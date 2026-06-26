package tools

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRead_PathValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   ReadInput
		wantSub string
	}{
		{"empty_file_path", ReadInput{FilePath: ""}, "file_path is required"},
		{"relative_path", ReadInput{FilePath: "foo.txt"}, "must be an absolute path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestReadHandler(DefaultReadConfig())
			_, _, err := h.handle(context.Background(), nil, tc.input)
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestRead_NotFound(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	_, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: "/nonexistent/path/to/file.txt"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("want not-exist error, got %v", err)
	}
}

func TestRead_Directory(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	_, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("want directory error, got %v", err)
	}
}

func TestRead_EmptyFile(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := writeTempFile(t, "empty.txt", nil)
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := textOf(t, res); !strings.Contains(got, "the file exists but the contents are empty") {
		t.Fatalf("want empty-file warning, got %q", got)
	}
}

func TestRead_BasicText(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := writeTempFile(t, "short.txt", []byte("hello\nworld\n"))
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "     1\thello\n     2\tworld\n"
	if got := textOf(t, res); got != want {
		t.Fatalf("text mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestRead_OffsetPastEnd(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := writeTempFile(t, "short.txt", []byte("a\nb\n"))
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path, Offset: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := textOf(t, res)
	if !strings.Contains(got, "shorter than the provided offset (10)") {
		t.Fatalf("want offset-past notice, got %q", got)
	}
	if !strings.Contains(got, "has 2 lines") {
		t.Fatalf("want total-lines info, got %q", got)
	}
}

func TestRead_OffsetAndLimit(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := writeTempFile(t, "lines.txt", []byte("a\nb\nc\nd\ne\n"))
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path, Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "     2\tb\n     3\tc\n"
	if got := textOf(t, res); got != want {
		t.Fatalf("offset/limit mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestRead_Truncation(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(ReadConfig{MaxLines: 3, LineCharCap: 2000})
	content := strings.Repeat("line\n", 10)
	path := writeTempFile(t, "long.txt", []byte(content))
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := textOf(t, res)
	if !strings.Contains(got, "has been truncated to the first 3 lines") {
		t.Fatalf("want truncation notice, got %q", got)
	}
}

func TestRead_LongLineTruncation(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(ReadConfig{MaxLines: 100, LineCharCap: 10})
	path := writeTempFile(t, "wide.txt", []byte(strings.Repeat("x", 100)+"\n"))
	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "     1\txxxxxxxxxx\n"
	if got := textOf(t, res); got != want {
		t.Fatalf("line truncation mismatch:\nwant=%q\ngot =%q", want, got)
	}
}

func TestRead_Image(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := filepath.Join(t.TempDir(), "tiny.png")
	writeTinyPNG(t, path)

	res, out, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(res.Content))
	}
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("want ImageContent, got %T", res.Content[0])
	}
	if img.MIMEType != "image/png" {
		t.Fatalf("want image/png, got %s", img.MIMEType)
	}
	if len(img.Data) == 0 {
		t.Fatalf("want image data, got empty")
	}
	if out != nil {
		t.Fatalf("structured output must be nil for image reads, got %+v", out)
	}
}

func TestRead_Notebook(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := filepath.Join(t.TempDir(), "simple.ipynb")
	writeSimpleNotebook(t, path)

	res, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := textOf(t, res)
	if !strings.Contains(got, `<cell id="cell-a" type="code">`) {
		t.Fatalf("want cell-a code cell, got:\n%s", got)
	}
	if !strings.Contains(got, "print('hello')") {
		t.Fatalf("want source line in output, got:\n%s", got)
	}
	if !strings.Contains(got, "<output>") {
		t.Fatalf("want output block, got:\n%s", got)
	}
}

func TestRead_PDFNotImplemented(t *testing.T) {
	t.Parallel()
	h := newTestReadHandler(DefaultReadConfig())
	path := filepath.Join(t.TempDir(), "tiny.pdf")
	writeTinyPDF(t, path)

	_, _, err := h.handle(context.Background(), nil, ReadInput{FilePath: path})
	if err == nil || !strings.Contains(err.Error(), "PDF reading is not yet implemented") {
		t.Fatalf("want PDF-not-implemented error, got %v", err)
	}
}

// --- helpers ---

func newTestReadHandler(cfg ReadConfig) *readHandler {
	return &readHandler{cfg: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("want TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func writeTinyPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeSimpleNotebook(t *testing.T, path string) {
	t.Helper()
	nb := map[string]interface{}{
		"cells": []map[string]interface{}{
			{
				"cell_type": "code",
				"id":        "cell-a",
				"source":    []string{"print('hello')\n"},
				"outputs": []map[string]interface{}{
					{
						"output_type": "stream",
						"text":        []string{"hello\n"},
					},
				},
			},
		},
		"metadata":       map[string]interface{}{},
		"nbformat":       4,
		"nbformat_minor": 5,
	}
	data, err := json.Marshal(nb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTinyPDF(t *testing.T, path string) {
	t.Helper()
	// Minimal byte sequence that mimetype's magic-byte sniffer detects as
	// application/pdf. Not a parseable PDF; sufficient for routing.
	data := []byte("%PDF-1.4\n%\xc7\xec\x8f\xa2\n1 0 obj<<>>endobj\nxref\n0 1\n0000000000 65535 f \ntrailer<</Root 1 0 R>>\n%%EOF\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
