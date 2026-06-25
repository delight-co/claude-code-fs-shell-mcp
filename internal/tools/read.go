// Package tools houses the MCP tool implementations.
package tools

import (
	"context"
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

// ReadOutput is intentionally empty so that structured content carries
// no payload. Image bytes are returned exclusively as MCP image content
// blocks, never duplicated into structuredContent that a client might
// serialize to text and forward to the model.
type ReadOutput struct{}

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
func RegisterRead(s *mcp.Server, cfg ReadConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &readHandler{cfg: cfg, logger: logger}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read",
		Description: readDescription,
	}, h.handle)
}

type readHandler struct {
	cfg    ReadConfig
	logger *slog.Logger
}

func (h *readHandler) handle(_ context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, ReadOutput, error) {
	out := ReadOutput{}

	if in.FilePath == "" {
		return nil, out, errors.New("file_path is required")
	}
	if !filepath.IsAbs(in.FilePath) {
		return nil, out, fmt.Errorf("file_path must be an absolute path, not relative: %s", in.FilePath)
	}
	clean := filepath.Clean(in.FilePath)

	info, err := os.Stat(clean)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, out, fmt.Errorf("file does not exist: %s", clean)
	case errors.Is(err, os.ErrPermission):
		return nil, out, fmt.Errorf("permission denied: %s", clean)
	case err != nil:
		return nil, out, fmt.Errorf("stat %s: %w", clean, err)
	}
	if info.IsDir() {
		return nil, out, fmt.Errorf("path is a directory, not a file: %s", clean)
	}

	mtype, err := mimetype.DetectFile(clean)
	if err != nil {
		return nil, out, fmt.Errorf("detect mime %s: %w", clean, err)
	}

	if mtype.Is("application/pdf") {
		return nil, out, fmt.Errorf("PDF reading is not yet implemented: %s", clean)
	}

	family, _, _ := strings.Cut(mtype.String(), "/")
	if family == "image" || family == "audio" {
		return h.readBinary(clean, mtype.String(), family)
	}

	if strings.EqualFold(filepath.Ext(clean), ".ipynb") {
		return h.readNotebook(clean)
	}

	return h.readText(clean, info, in.Offset, in.Limit)
}

func (h *readHandler) readText(path string, info os.FileInfo, offset, limit int) (*mcp.CallToolResult, ReadOutput, error) {
	out := ReadOutput{}

	if info.Size() == 0 {
		return textResult("<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"), out, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, out, fmt.Errorf("read %s: %w", path, err)
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
		return textResult(msg), out, nil
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

	return textResult(b.String()), out, nil
}

func (h *readHandler) readBinary(path, mime, family string) (*mcp.CallToolResult, ReadOutput, error) {
	out := ReadOutput{}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, out, fmt.Errorf("read %s: %w", path, err)
	}

	var content mcp.Content
	switch family {
	case "image":
		content = &mcp.ImageContent{Data: data, MIMEType: mime}
	case "audio":
		content = &mcp.AudioContent{Data: data, MIMEType: mime}
	default:
		return nil, out, fmt.Errorf("unsupported binary family: %s", family)
	}

	return &mcp.CallToolResult{Content: []mcp.Content{content}}, out, nil
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

func (h *readHandler) readNotebook(path string) (*mcp.CallToolResult, ReadOutput, error) {
	out := ReadOutput{}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, out, fmt.Errorf("read %s: %w", path, err)
	}

	var nb notebook
	if err := json.Unmarshal(raw, &nb); err != nil {
		return nil, out, fmt.Errorf("parse notebook %s: %w", path, err)
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

	return textResult(b.String()), out, nil
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
