// Package tools houses the MCP tool implementations.
package tools

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 here is a content-change detector, not a security primitive.
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMaxLines    = 2000
	defaultLineCharCap = 2000
)

// ReadInput is the JSON input schema for the read tool.
type ReadInput struct {
	FilePath string `json:"file_path" jsonschema:"absolute path to the file to read"`
	Offset   int    `json:"offset,omitempty" jsonschema:"1-based line number to start reading from"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum number of lines to return"`
	Pages    string `json:"pages,omitempty" jsonschema:"PDF page range like '1-5'; max 20 pages per call"`
}

// ReadConfig configures the read tool's tunable behaviour. The defaults
// match the values the Claude Code CLI built-in advertises in its
// prompt-level descriptions.
type ReadConfig struct {
	MaxLines    int
	LineCharCap int
}

// DefaultReadConfig returns the default configuration: 2000-line cap and
// 2000-character per-line truncation.
func DefaultReadConfig() ReadConfig {
	return ReadConfig{
		MaxLines:    defaultMaxLines,
		LineCharCap: defaultLineCharCap,
	}
}

const readDescription = `Reads a file from the local filesystem.

- ` + "`file_path`" + ` must be an absolute path.
- Reads up to 2000 lines by default (configurable). Lines are returned in cat -n style: the line number, a tab, then the line.
- Images (PNG, JPG, ...) are returned as visual content the model can see. Jupyter notebooks (.ipynb) are returned as cells with their outputs.
- Reading a directory, a missing file, or an empty file returns an error or a notice rather than file contents.`

// RegisterRead adds the read tool to the given MCP server using the
// provided configuration.
//
// The handler uses `any` as its output type so the SDK does not populate
// the response's structuredContent field. All payload (text, images,
// notebook renderings) is returned via Content blocks only. Some MCP
// clients prefer structuredContent over content when both are present,
// which would otherwise hide the file contents behind an empty "{}".
//
// The seed parameter, when non-nil, is invoked after every successful
// read so the per-session read-tracking state is populated for the Write
// tool family (read-before-overwrite, modified-since-read).
func RegisterRead(s *mcp.Server, cfg ReadConfig, logger *slog.Logger, seed ReadStateSeed) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &readHandler{cfg: cfg, logger: logger, seed: seed}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read",
		Description: readDescription,
	}, h.handle)
}

type readHandler struct {
	cfg    ReadConfig
	logger *slog.Logger
	seed   ReadStateSeed
}

func (h *readHandler) handle(_ context.Context, req *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, any, error) {
	if in.FilePath == "" {
		return nil, nil, errors.New("file_path is required")
	}
	if !filepath.IsAbs(in.FilePath) {
		return nil, nil, fmt.Errorf("file_path must be an absolute path, not relative: %s", in.FilePath)
	}
	clean := filepath.Clean(in.FilePath)

	info, err := os.Stat(clean)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("file does not exist: %s", clean)
	case errors.Is(err, os.ErrPermission):
		return nil, nil, fmt.Errorf("permission denied: %s", clean)
	case err != nil:
		return nil, nil, fmt.Errorf("stat %s: %w", clean, err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("path is a directory, not a file: %s", clean)
	}

	mtype, err := mimetype.DetectFile(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("detect mime %s: %w", clean, err)
	}

	if mtype.Is("application/pdf") {
		return h.readPDF(req, clean, info, in.Pages)
	}

	family, _, _ := strings.Cut(mtype.String(), "/")
	if family == "image" || family == "audio" {
		return h.readBinary(req, clean, info, mtype.String(), family)
	}

	if strings.EqualFold(filepath.Ext(clean), ".ipynb") {
		return h.readNotebook(req, clean, info)
	}

	return h.readText(req, clean, info, in.Offset, in.Limit)
}

func (h *readHandler) readText(req *mcp.CallToolRequest, path string, info os.FileInfo, offset, limit int) (*mcp.CallToolResult, any, error) {
	if info.Size() == 0 {
		h.seedReadEntry(req, path, nil, info, offset, limit)
		return textResult("<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"), nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(raw)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	totalLines := len(lines)

	startLine := 1
	if offset > 0 {
		startLine = offset
	}
	if startLine > totalLines {
		msg := fmt.Sprintf(
			"<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>",
			startLine, totalLines,
		)
		h.seedReadEntry(req, path, raw, info, offset, limit)
		return textResult(msg), nil, nil
	}

	endLine := totalLines
	truncated := false
	switch {
	case limit > 0:
		end := startLine + limit - 1
		if end < totalLines {
			endLine = end
		}
	case offset == 0 && totalLines > h.cfg.MaxLines:
		endLine = h.cfg.MaxLines
		truncated = true
	}

	var b strings.Builder
	for i := startLine; i <= endLine; i++ {
		line := lines[i-1]
		if len(line) > h.cfg.LineCharCap {
			line = line[:h.cfg.LineCharCap]
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i, line)
	}

	if truncated {
		fmt.Fprintf(
			&b,
			"\nNote: The file %s was too large and has been truncated to the first %d lines. Don't tell the user about this truncation. Use Read to read more of the file if you need.",
			path, h.cfg.MaxLines,
		)
	}

	h.seedReadEntry(req, path, raw, info, offset, limit)
	return textResult(b.String()), nil, nil
}

func (h *readHandler) readBinary(req *mcp.CallToolRequest, path string, info os.FileInfo, mime, family string) (*mcp.CallToolResult, any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	var content mcp.Content
	switch family {
	case "image":
		content = &mcp.ImageContent{Data: data, MIMEType: mime}
	case "audio":
		content = &mcp.AudioContent{Data: data, MIMEType: mime}
	default:
		return nil, nil, fmt.Errorf("unsupported binary family: %s", family)
	}

	h.seedReadEntry(req, path, data, info, 0, 0)
	return &mcp.CallToolResult{Content: []mcp.Content{content}}, nil, nil
}

type notebook struct {
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	ID       string               `json:"id,omitempty"`
	CellType string               `json:"cell_type"`
	Source   json.RawMessage      `json:"source"`
	Outputs  []notebookCellOutput `json:"outputs,omitempty"`
}

type notebookCellOutput struct {
	OutputType string                 `json:"output_type"`
	Text       json.RawMessage        `json:"text,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`
}

func (h *readHandler) readNotebook(req *mcp.CallToolRequest, path string, info os.FileInfo) (*mcp.CallToolResult, any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	var nb notebook
	if err := json.Unmarshal(raw, &nb); err != nil {
		return nil, nil, fmt.Errorf("parse notebook %s: %w", path, err)
	}

	var b strings.Builder
	for i, cell := range nb.Cells {
		id := cell.ID
		if id == "" {
			id = fmt.Sprintf("cell-%d", i)
		}
		fmt.Fprintf(&b, "<cell id=%q type=%q>\n", id, cell.CellType)
		b.WriteString(joinRawSource(cell.Source))
		b.WriteString("\n")
		for _, o := range cell.Outputs {
			if len(o.Text) > 0 {
				b.WriteString("<output>\n")
				b.WriteString(joinRawSource(o.Text))
				b.WriteString("\n</output>\n")
			}
			for mime, val := range o.Data {
				switch {
				case strings.HasPrefix(mime, "image/"):
					fmt.Fprintf(&b, "<output type=%q>[binary image omitted]</output>\n", mime)
				case mime == "text/plain":
					fmt.Fprintf(&b, "<output type=%q>\n%s\n</output>\n", mime, joinAny(val))
				default:
					fmt.Fprintf(&b, "<output type=%q>[unhandled mime]</output>\n", mime)
				}
			}
		}
		b.WriteString("</cell>\n")
	}

	h.seedReadEntry(req, path, raw, info, 0, 0)
	return textResult(b.String()), nil, nil
}

// seedReadEntry records a successful read in the per-session
// read-tracking registry. It is a no-op when the seed is unset, the
// request has no associated session, or the session id is empty
// (typical of bare unit tests). For full reads (offset == 0 and
// limit == 0), the LF-normalised content and its SHA-1 base64url digest
// are stored so the Write tool can run the modified-since-read
// content-equality fallback. For partial reads, those two fields are
// left zero: the Write tool refuses unconditionally when a partial
// read's mtime has advanced.
func (h *readHandler) seedReadEntry(req *mcp.CallToolRequest, path string, raw []byte, info os.FileInfo, offset, limit int) {
	if h.seed == nil {
		return
	}
	sessionID := ""
	if req != nil && req.Session != nil {
		sessionID = req.Session.ID()
	}
	if sessionID == "" {
		return
	}

	var entryContent []byte
	var entryHash string
	if offset == 0 && limit == 0 {
		normalised := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		entryContent = normalised
		entryHash = hashContent(normalised)
	}

	h.seed.Seed(sessionID, path, ReadEntry{
		Content:       entryContent,
		ContentHash:   entryHash,
		ModTimeMillis: info.ModTime().UnixMilli(),
		Offset:        offset,
		Limit:         limit,
	})
}

func hashContent(b []byte) string {
	sum := sha1.Sum(b) //nolint:gosec // content-change detector, not security.
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func joinRawSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, "")
	}
	return string(raw)
}

func joinAny(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []interface{}:
		parts := make([]string, 0, len(val))
		for _, p := range val {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "")
	}
	return fmt.Sprint(v)
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
