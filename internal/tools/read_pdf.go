package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PDF tool constants matching the upstream Read tool's parts-mode
// pipeline (cli.js v2.1.195 NMo / H8 / Gce).
const (
	pdftoppmTimeout     = 120 * time.Second
	pdftoppmDPI         = 100
	maxPagesPerRequest  = 20
	imageMaxDimension   = 2000
	pdfPartsModeMaxSize = int64(100 * 1024 * 1024) // 100 MiB, matches upstream FQr
)

// PDF-specific error wordings, byte-exact with the upstream Read tool's
// strings. See docs/spec/read.md "PDF-specific errors" subsection.
const (
	errPDFInvalidPagesSyntaxFmt    = "Invalid pages parameter: %q. Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed."
	errPDFPageSpanExceedsFmt       = "Page range %q exceeds maximum of 20 pages per request. Please use a smaller range."
	errPDFPagesRequiredAdapted     = "Reading full PDFs is not supported by this MCP server. Use the pages parameter to read specific page ranges (e.g., pages: \"1-5\", maximum 20 pages per request)."
	errPDFPdftoppmMissing          = "pdftoppm is not installed. Install poppler-utils (e.g. `brew install poppler` or `apt-get install poppler-utils`) to enable PDF page rendering."
	errPDFPasswordProtected        = "PDF is password-protected. Please provide an unprotected version."
	errPDFCorrupted                = "PDF file is corrupted or invalid."
	errPDFPageOutOfRangeFmt        = "Requested %s is outside the document (PDF has %d page(s)). Use a range within 1-%d, maximum 20 pages per request (e.g. pages: \"1-%d\")."
	errPDFRenderIOErrFmt           = "Could not render PDF: %s"
	errPDFGenericFailureFmt        = "pdftoppm failed: %s"
	errPDFNoOutputProduced         = "pdftoppm produced no output pages. The PDF may be invalid."
	errPDFEmptyFmt                 = "PDF file is empty: %s"
	errPDFNotRegularFmt            = "Path is not a regular file: %s"
	errPDFTooLargeForExtractionFmt = "PDF file exceeds maximum allowed size for text extraction (%s)."
)

// pdftoppmOnce memoises the PATH lookup for pdftoppm. The first PDF
// read probes lazily; subsequent reads reuse the cached result.
var (
	pdftoppmOnce sync.Once
	pdftoppmPath string
	pdftoppmErr  error
)

// resolvePdftoppm returns an absolute pdftoppm path or a spec-pinned
// "not installed" error. Safe for concurrent callers.
func resolvePdftoppm() (string, error) {
	pdftoppmOnce.Do(func() {
		p, err := exec.LookPath("pdftoppm")
		if err != nil {
			pdftoppmErr = errors.New(errPDFPdftoppmMissing) //nolint:staticcheck // spec-pinned wording ends with period.
			return
		}
		pdftoppmPath = p
	})
	return pdftoppmPath, pdftoppmErr
}

// pagesParseResult holds a parsed pages parameter.
type pagesParseResult struct {
	FirstPage int
	LastPage  int // ignored when OpenRange is true
	OpenRange bool
}

// parsePages parses the upstream Read tool's pages syntax:
//   - "3"   -> {FirstPage:3, LastPage:3}
//   - "1-5" -> {FirstPage:1, LastPage:5}
//   - "5-"  -> {FirstPage:5, OpenRange:true}
//
// Multiple ranges (e.g. "1-5,8-10") are not accepted (single range
// only). Returns ok=false on malformed input; the caller maps that
// to the invalid-pages-syntax error.
func parsePages(s string) (pagesParseResult, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return pagesParseResult{}, false
	}
	if strings.HasSuffix(t, "-") {
		v, err := strconv.Atoi(strings.TrimSuffix(t, "-"))
		if err != nil || v < 1 {
			return pagesParseResult{}, false
		}
		return pagesParseResult{FirstPage: v, OpenRange: true}, true
	}
	if dash := strings.Index(t, "-"); dash >= 0 {
		first, err1 := strconv.Atoi(t[:dash])
		last, err2 := strconv.Atoi(t[dash+1:])
		if err1 != nil || err2 != nil || first < 1 || last < 1 || last < first {
			return pagesParseResult{}, false
		}
		return pagesParseResult{FirstPage: first, LastPage: last}, true
	}
	v, err := strconv.Atoi(t)
	if err != nil || v < 1 {
		return pagesParseResult{}, false
	}
	return pagesParseResult{FirstPage: v, LastPage: v}, true
}

// pagesSpan returns the number of pages the parsed range covers. An
// open range is treated as exceeding the cap (matching the upstream's
// Gce+1 > Gce always-true validation).
func pagesSpan(p pagesParseResult) int {
	if p.OpenRange {
		return maxPagesPerRequest + 1
	}
	return p.LastPage - p.FirstPage + 1
}

// pagesHumanReadable mirrors the upstream pCf() helper.
func pagesHumanReadable(p pagesParseResult) string {
	if p.OpenRange {
		return fmt.Sprintf("page range %d-end", p.FirstPage)
	}
	if p.FirstPage == p.LastPage {
		return fmt.Sprintf("page %d", p.FirstPage)
	}
	return fmt.Sprintf("page range %d-%d", p.FirstPage, p.LastPage)
}

// readPDF is the parts-mode PDF read entry point. See docs/spec/read.md
// "PDFs" subsection for the contract.
func (h *readHandler) readPDF(req *mcp.CallToolRequest, path string, info os.FileInfo, pages string) (*mcp.CallToolResult, any, error) {
	if pages == "" {
		return nil, nil, errors.New(errPDFPagesRequiredAdapted) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	parsed, ok := parsePages(pages)
	if !ok {
		return nil, nil, fmt.Errorf(errPDFInvalidPagesSyntaxFmt, pages) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	if pagesSpan(parsed) > maxPagesPerRequest {
		return nil, nil, fmt.Errorf(errPDFPageSpanExceedsFmt, pages) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf(errPDFNotRegularFmt, path) //nolint:staticcheck // spec-pinned wording starts with capital.
	}
	if info.Size() == 0 {
		return nil, nil, fmt.Errorf(errPDFEmptyFmt, path) //nolint:staticcheck // spec-pinned wording starts with capital.
	}
	if info.Size() > pdfPartsModeMaxSize {
		return nil, nil, fmt.Errorf(errPDFTooLargeForExtractionFmt, formatBytesBinary(info.Size())) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	binPath, err := resolvePdftoppm()
	if err != nil {
		return nil, nil, err
	}

	tmpDir, err := os.MkdirTemp("", "ccfs-pdf-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create pdf temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	args := []string{"-jpeg", "-r", strconv.Itoa(pdftoppmDPI), "-f", strconv.Itoa(parsed.FirstPage)}
	if !parsed.OpenRange {
		args = append(args, "-l", strconv.Itoa(parsed.LastPage))
	}
	args = append(args, path, filepath.Join(tmpDir, "page"))

	ctx, cancel := context.WithTimeout(context.Background(), pdftoppmTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...) //nolint:gosec // binPath is from exec.LookPath; args are constructed from validated input.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		if pdfErr := classifyPdftoppmError(stderr.String(), parsed); pdfErr != nil {
			return nil, nil, pdfErr
		}
		return nil, nil, fmt.Errorf(errPDFGenericFailureFmt, strings.TrimSpace(stderr.String())) //nolint:staticcheck // spec-pinned wording.
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read pdf output dir: %w", err)
	}
	var jpgs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jpg") {
			jpgs = append(jpgs, filepath.Join(tmpDir, e.Name()))
		}
	}
	if len(jpgs) == 0 {
		return nil, nil, errors.New(errPDFNoOutputProduced) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	sort.Strings(jpgs)

	blocks := make([]mcp.Content, 0, len(jpgs)+1)
	blocks = append(blocks, &mcp.TextContent{
		Text: fmt.Sprintf("PDF pages extracted: %d page(s) from %s (%s)",
			len(jpgs), path, formatBytesBinary(info.Size())),
	})
	for _, jpg := range jpgs {
		data, readErr := os.ReadFile(jpg)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read jpg %s: %w", jpg, readErr)
		}
		capped, capErr := capJPEG(data)
		if capErr != nil {
			return nil, nil, fmt.Errorf("resize jpg %s: %w", jpg, capErr)
		}
		blocks = append(blocks, &mcp.ImageContent{
			Data:     capped,
			MIMEType: "image/jpeg",
		})
	}

	h.seedReadEntry(req, path, nil, info, 0, 0)
	return &mcp.CallToolResult{Content: blocks}, nil, nil
}

// classifyPdftoppmError maps pdftoppm stderr text to a spec-pinned
// error. Returns nil when no specific pattern matches; the caller then
// falls back to the generic-failure wording.
func classifyPdftoppmError(stderr string, p pagesParseResult) error {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "password") {
		return errors.New(errPDFPasswordProtected) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if strings.Contains(stderr, "Wrong page range given") {
		if pageCount := extractPdfPageCount(stderr); pageCount > 0 {
			cap := pageCount
			if cap > maxPagesPerRequest {
				cap = maxPagesPerRequest
			}
			return fmt.Errorf(errPDFPageOutOfRangeFmt, pagesHumanReadable(p), pageCount, pageCount, cap) //nolint:staticcheck // spec-pinned wording ends with period.
		}
	}
	if strings.Contains(lower, "damaged") || strings.Contains(lower, "corrupt") ||
		strings.Contains(stderr, "Couldn't find trailer dictionary") ||
		strings.Contains(stderr, "read xref table") {
		return errors.New(errPDFCorrupted) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	firstLine := strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])
	if (strings.HasPrefix(firstLine, "I/O Error:") || strings.HasPrefix(firstLine, "Permission Error:")) &&
		!strings.Contains(stderr, "Command Line Error") && !strings.Contains(stderr, "Internal Error") {
		return fmt.Errorf(errPDFRenderIOErrFmt, firstLine) //nolint:staticcheck // spec-pinned wording.
	}
	return nil
}

// extractPdfPageCount parses "last page (NNN)" out of pdftoppm stderr.
// Returns 0 when the pattern is not present (caller falls back).
func extractPdfPageCount(stderr string) int {
	const marker = "last page ("
	idx := strings.Index(stderr, marker)
	if idx < 0 {
		return 0
	}
	rest := stderr[idx+len(marker):]
	end := strings.Index(rest, ")")
	if end < 0 {
		return 0
	}
	v, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return v
}

// capJPEG resizes the given JPEG bytes to fit within imageMaxDimension
// on both axes (preserving aspect ratio) and re-encodes as JPEG. When
// the source already fits within the cap, returns the input unchanged.
// The byte-budget compression loop the upstream sharp pipeline uses
// (target 500 KB / cap 5 MB base64) is not implemented; see Known gaps
// in docs/spec/read.md.
func capJPEG(data []byte) ([]byte, error) {
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() <= imageMaxDimension && b.Dy() <= imageMaxDimension {
		return data, nil
	}
	resized := imaging.Fit(img, imageMaxDimension, imageMaxDimension, imaging.Lanczos)
	var buf bytes.Buffer
	if err := imaging.Encode(&buf, resized, imaging.JPEG); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
