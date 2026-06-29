package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParsePages(t *testing.T) {
	tests := []struct {
		in        string
		wantOK    bool
		wantFirst int
		wantLast  int
		wantOpen  bool
	}{
		{"3", true, 3, 3, false},
		{"1-5", true, 1, 5, false},
		{"5-", true, 5, 0, true},
		{"10-20", true, 10, 20, false},
		{"", false, 0, 0, false},
		{"  ", false, 0, 0, false},
		{"abc", false, 0, 0, false},
		{"0", false, 0, 0, false},
		{"-3", false, 0, 0, false},
		{"3-1", false, 0, 0, false},
		{"1-5,8-10", false, 0, 0, false},
		{"1-", true, 1, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parsePages(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parsePages(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.FirstPage != tt.wantFirst || got.LastPage != tt.wantLast || got.OpenRange != tt.wantOpen {
				t.Errorf("parsePages(%q) = %+v, want FirstPage=%d LastPage=%d OpenRange=%v",
					tt.in, got, tt.wantFirst, tt.wantLast, tt.wantOpen)
			}
		})
	}
}

func TestPagesSpan(t *testing.T) {
	tests := []struct {
		name string
		in   pagesParseResult
		want int
	}{
		{"single page", pagesParseResult{FirstPage: 3, LastPage: 3}, 1},
		{"closed range", pagesParseResult{FirstPage: 1, LastPage: 5}, 5},
		{"open range always exceeds", pagesParseResult{FirstPage: 5, OpenRange: true}, maxPagesPerRequest + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pagesSpan(tt.in); got != tt.want {
				t.Errorf("pagesSpan = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPagesHumanReadable(t *testing.T) {
	tests := []struct {
		name string
		in   pagesParseResult
		want string
	}{
		{"single page", pagesParseResult{FirstPage: 3, LastPage: 3}, "page 3"},
		{"closed range", pagesParseResult{FirstPage: 1, LastPage: 5}, "page range 1-5"},
		{"open range", pagesParseResult{FirstPage: 5, OpenRange: true}, "page range 5-end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pagesHumanReadable(tt.in); got != tt.want {
				t.Errorf("pagesHumanReadable = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadPDF_PagesRequired(t *testing.T) {
	h := newPDFTestHandler()
	info := mustStatTestPDF(t)
	_, _, err := h.readPDF(nil, testPDFPath(t), info, "")
	requireErrorContains(t, err, "Reading full PDFs is not supported by this MCP server")
}

func TestReadPDF_InvalidPages(t *testing.T) {
	h := newPDFTestHandler()
	info := mustStatTestPDF(t)
	_, _, err := h.readPDF(nil, testPDFPath(t), info, "abc")
	requireErrorContains(t, err, `Invalid pages parameter: "abc"`)
}

func TestReadPDF_PageSpanExceeds(t *testing.T) {
	h := newPDFTestHandler()
	info := mustStatTestPDF(t)
	_, _, err := h.readPDF(nil, testPDFPath(t), info, "1-25")
	requireErrorContains(t, err, `Page range "1-25" exceeds maximum of 20 pages per request`)
}

func TestReadPDF_OpenRangeRejected(t *testing.T) {
	h := newPDFTestHandler()
	info := mustStatTestPDF(t)
	_, _, err := h.readPDF(nil, testPDFPath(t), info, "5-")
	requireErrorContains(t, err, `Page range "5-" exceeds maximum of 20 pages per request`)
}

func TestReadPDF_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not available; skipping integration test")
	}
	resetPdftoppmCacheForTest()

	h := newPDFTestHandler()
	info := mustStatTestPDF(t)
	res, _, err := h.readPDF(nil, testPDFPath(t), info, "1")
	if err != nil {
		t.Fatalf("readPDF: %v", err)
	}
	if len(res.Content) < 2 {
		t.Fatalf("want at least 2 content blocks (text + image), got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first block should be TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "PDF pages extracted: 1 page(s)") {
		t.Errorf("summary text wrong: %q", tc.Text)
	}
	ic, ok := res.Content[1].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("second block should be ImageContent, got %T", res.Content[1])
	}
	if ic.MIMEType != "image/jpeg" {
		t.Errorf("image MIME = %q, want image/jpeg", ic.MIMEType)
	}
	if len(ic.Data) == 0 {
		t.Errorf("image data is empty")
	}
}

// --- test helpers ---

func newPDFTestHandler() *readHandler {
	return &readHandler{cfg: DefaultReadConfig()}
}

func testPDFPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata/dummy.pdf")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	return abs
}

func mustStatTestPDF(t *testing.T) os.FileInfo {
	t.Helper()
	info, err := os.Stat(testPDFPath(t))
	if err != nil {
		t.Fatalf("stat testdata/dummy.pdf: %v", err)
	}
	return info
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}

// resetPdftoppmCacheForTest resets the memo so an integration test
// re-probes pdftoppm. Sequential tests only — do not call concurrently.
func resetPdftoppmCacheForTest() {
	pdftoppmOnce = sync.Once{}
	pdftoppmPath = ""
	pdftoppmErr = nil
}
