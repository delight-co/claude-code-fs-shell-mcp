package server_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	registry := server.NewReadTrackingRegistry(256, time.Minute, logger)
	h, err := server.New(logger, registry)
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

// TestReadSeedsRegistryUnderSession verifies that a successful read
// through a live MCP session populates the read-tracking registry under
// the session's MCP session id, with the metadata the Write tool family
// will consult (content hash, mtime, offset / limit).
func TestReadSeedsRegistryUnderSession(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := server.NewReadTrackingRegistry(256, time.Minute, logger)

	h, err := server.New(logger, registry)
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

	sessionID := cs.ID()
	if sessionID == "" {
		t.Fatal("expected client session id, got empty string")
	}

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

	entry, ok := registry.Get(sessionID, path)
	if !ok {
		t.Fatalf("registry should contain an entry for (sessionID=%q, path=%q) after a successful Read", sessionID, path)
	}
	if entry.ContentHash == "" {
		t.Errorf("ContentHash should be populated for a full read, got empty")
	}
	if entry.ModTimeMillis == 0 {
		t.Errorf("ModTimeMillis should be populated, got 0")
	}
	if entry.Offset != 0 || entry.Limit != 0 {
		t.Errorf("full read should have Offset=0 Limit=0, got Offset=%d Limit=%d", entry.Offset, entry.Limit)
	}
	if !bytes.Equal(entry.Content, []byte("hello\nworld\n")) {
		t.Errorf("Content = %q, want %q", entry.Content, "hello\nworld\n")
	}
}

// TestReadSeedsRegistryPartialRead verifies that a partial Read (with
// offset/limit) still creates a registry entry, but with Content and
// ContentHash zero so the Write tool's modified-since-read check
// refuses unconditionally if mtime advances.
func TestReadSeedsRegistryPartialRead(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := server.NewReadTrackingRegistry(256, time.Minute, logger)

	h, err := server.New(logger, registry)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := mcp.NewClient(&mcp.Implementation{Name: "ccfs-partial-test", Version: "v0.0.0"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	sessionID := cs.ID()

	path := filepath.Join(t.TempDir(), "many.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"file_path": path, "offset": 2, "limit": 2},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	entry, ok := registry.Get(sessionID, path)
	if !ok {
		t.Fatalf("registry should contain an entry for the partial Read")
	}
	if entry.Offset != 2 || entry.Limit != 2 {
		t.Errorf("partial read should record Offset=2 Limit=2, got Offset=%d Limit=%d", entry.Offset, entry.Limit)
	}
	if entry.ContentHash != "" {
		t.Errorf("ContentHash should be empty for a partial read, got %q", entry.ContentHash)
	}
	if len(entry.Content) != 0 {
		t.Errorf("Content should be empty for a partial read, got %q", entry.Content)
	}
	if entry.ModTimeMillis == 0 {
		t.Errorf("ModTimeMillis should still be populated, got 0")
	}
}

// TestSessionsAreIsolated verifies that two MCP clients sharing the
// server have independent read-tracking entries: a Read performed by
// one client must not appear in the other client's session.
func TestSessionsAreIsolated(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := server.NewReadTrackingRegistry(256, time.Minute, logger)

	h, err := server.New(logger, registry)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	c1 := mcp.NewClient(&mcp.Implementation{Name: "client-1", Version: "v0"}, nil)
	cs1, err := c1.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("client-1 connect: %v", err)
	}
	defer func() { _ = cs1.Close() }()

	c2 := mcp.NewClient(&mcp.Implementation{Name: "client-2", Version: "v0"}, nil)
	cs2, err := c2.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("client-2 connect: %v", err)
	}
	defer func() { _ = cs2.Close() }()

	if cs1.ID() == cs2.ID() || cs1.ID() == "" {
		t.Fatalf("expected distinct non-empty session ids, got %q and %q", cs1.ID(), cs2.ID())
	}

	path := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(path, []byte("data\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := cs1.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"file_path": path},
	}); err != nil {
		t.Fatalf("cs1 CallTool: %v", err)
	}

	if _, ok := registry.Get(cs1.ID(), path); !ok {
		t.Errorf("cs1 should have a registry entry for %q", path)
	}
	if _, ok := registry.Get(cs2.ID(), path); ok {
		t.Errorf("cs2 should NOT have a registry entry for %q (sessions must be isolated)", path)
	}
}
