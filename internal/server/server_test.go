package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/delight-co/claude-code-fs-shell-mcp/internal/server"
)

// TestReadDoesNotSendStructuredContent verifies that successful read tool
// calls return their payload only through MCP Content blocks and never set
// the StructuredContent field.
//
// Background: some MCP clients (including the Claude Code CLI) prefer
// StructuredContent over Content when both are present. If the server sends
// an empty StructuredContent object alongside the real Content, those clients
// display "{}" instead of the file contents.
func TestReadDoesNotSendStructuredContent(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	h, err := server.New(logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := mcp.NewClient(&mcp.Implementation{Name: "ccfs-integration-test", Version: "v0.0.0"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	path := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"file_path": path},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}

	if res.StructuredContent != nil {
		t.Fatalf("structuredContent must not be set, got: %#v", res.StructuredContent)
	}

	if len(res.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("want TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "hello") || !strings.Contains(tc.Text, "world") {
		t.Fatalf("text missing expected lines, got: %q", tc.Text)
	}
}
