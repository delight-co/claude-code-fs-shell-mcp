package tools

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestBashHandler(t *testing.T) *bashHandler {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return &bashHandler{
		cfg: BashConfig{
			DefaultTimeoutMs: 120_000,
			MaxTimeoutMs:     600_000,
			OutputCapChars:   30_000,
			OriginalCwd:      cwd,
		},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		cwdState: newBashCwdState(),
	}
}

func extractText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatalf("nil result")
	}
	if len(res.Content) == 0 {
		t.Fatalf("empty content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first content is not TextContent: %T", res.Content[0])
	}
	return tc.Text
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{100 * time.Millisecond, "100ms"},
		{2 * time.Second, "2s"},
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m"},
		{90 * time.Second, "1m"},
		{time.Hour, "1h"},
		{time.Hour + 30*time.Minute, "1h30m"},
		{2 * time.Hour, "2h"},
	}
	for _, c := range cases {
		got := formatDuration(c.d)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "'hello'"},
		{"a b c", "'a b c'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, c := range cases {
		got := shellQuote(c.in)
		if got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAppendLine(t *testing.T) {
	cases := []struct {
		existing, addition, want string
	}{
		{"", "added", "added"},
		{"first", "second", "first\nsecond"},
		{"a\nb", "c", "a\nb\nc"},
	}
	for _, c := range cases {
		got := appendLine(c.existing, c.addition)
		if got != c.want {
			t.Errorf("appendLine(%q, %q) = %q, want %q", c.existing, c.addition, got, c.want)
		}
	}
}

func TestIsSubpath(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a", "/a/b", false},
		{"/a/b/c/d", "/a", true},
	}
	for _, c := range cases {
		got := isSubpath(c.child, c.parent)
		if got != c.want {
			t.Errorf("isSubpath(%q, %q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}

func TestRecoverCwd(t *testing.T) {
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "deleted", "subdir")
	got := recoverCwd(missing)
	if got != tmpDir {
		t.Errorf("recoverCwd(%q) = %q, want %q", missing, got, tmpDir)
	}
	got = recoverCwd(tmpDir)
	if got != tmpDir {
		t.Errorf("recoverCwd(%q) = %q, want %q", tmpDir, got, tmpDir)
	}
}

func TestBashCwdState(t *testing.T) {
	s := newBashCwdState()
	if got := s.get("sid-1"); got != "" {
		t.Errorf("get on empty session: %q, want empty", got)
	}
	s.set("sid-1", "/path/to/dir")
	if got := s.get("sid-1"); got != "/path/to/dir" {
		t.Errorf("get after set: %q, want /path/to/dir", got)
	}
	s.set("sid-2", "/other")
	if got := s.get("sid-1"); got != "/path/to/dir" {
		t.Errorf("session isolation broken: %q", got)
	}
	s.set("", "ignored")
	if got := s.get(""); got != "" {
		t.Errorf("empty session id should not store: %q", got)
	}
}

func TestBuildEnvStripsAuth(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "secret-token")
	t.Setenv("CLAUDE_CODE_SUBSCRIPTION_TYPE", "pro")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("MY_PUBLIC_VAR", "kept")

	h := newTestBashHandler(t)
	env := h.buildEnv("/bin/bash")

	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=") {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN leaked: %q", kv)
		}
		if strings.HasPrefix(kv, "CLAUDE_CODE_SUBSCRIPTION_TYPE=") {
			t.Errorf("CLAUDE_CODE_SUBSCRIPTION_TYPE leaked: %q", kv)
		}
		if strings.HasPrefix(kv, "OTEL_") {
			t.Errorf("OTEL var leaked: %q", kv)
		}
	}
	hasMyVar := false
	hasGitEditor := false
	hasShell := false
	for _, kv := range env {
		switch {
		case kv == "MY_PUBLIC_VAR=kept":
			hasMyVar = true
		case kv == "GIT_EDITOR=true":
			hasGitEditor = true
		case kv == "SHELL=/bin/bash":
			hasShell = true
		}
	}
	if !hasMyVar {
		t.Errorf("non-auth env var was stripped")
	}
	if !hasGitEditor {
		t.Errorf("GIT_EDITOR=true not in env")
	}
	if !hasShell {
		t.Errorf("SHELL=/bin/bash not in env")
	}
}

func TestDetectShell(t *testing.T) {
	h := newTestBashHandler(t)
	shell, err := h.detectShell()
	if err != nil {
		t.Skipf("no shell available in test env: %v", err)
	}
	if !strings.Contains(shell, "bash") && !strings.Contains(shell, "zsh") {
		t.Errorf("detected shell %q is neither bash nor zsh", shell)
	}
}

func TestIsInsideProject(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := t.TempDir()

	h := &bashHandler{cfg: BashConfig{OriginalCwd: tmpDir}}
	if !h.isInsideProject(tmpDir) {
		t.Errorf("project root should be inside project")
	}
	if !h.isInsideProject(subDir) {
		t.Errorf("subdir should be inside project")
	}
	if h.isInsideProject(outside) {
		t.Errorf("unrelated dir should not be inside project")
	}
}

func TestHandleEmptyCommandRejected(t *testing.T) {
	h := newTestBashHandler(t)
	_, _, err := h.handle(context.Background(), nil, BashInput{Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if err.Error() != errBashCommandEmpty {
		t.Errorf("wrong wording: %q", err.Error())
	}
}

func TestHandleSimpleCommand(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("no bash available: %v", err)
	}
	h := newTestBashHandler(t)
	res, _, err := h.handle(context.Background(), nil, BashInput{Command: "echo hello"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", text)
	}
}

func TestHandleNonZeroExit(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("no bash available: %v", err)
	}
	h := newTestBashHandler(t)
	res, _, err := h.handle(context.Background(), nil, BashInput{Command: "exit 7"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Exit code 7") {
		t.Errorf("expected 'Exit code 7', got %q", text)
	}
}

func TestHandleRunInBackgroundFallback(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("no bash available: %v", err)
	}
	h := newTestBashHandler(t)
	res, _, err := h.handle(context.Background(), nil, BashInput{Command: "echo foreground", RunInBackground: true})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, noticeRunInBgFallback) {
		t.Errorf("expected fallback notice, got %q", text)
	}
}

func TestHandleTimeout(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("no bash available: %v", err)
	}
	h := newTestBashHandler(t)
	res, _, err := h.handle(context.Background(), nil, BashInput{Command: "sleep 5", Timeout: 200})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Command timed out after") {
		t.Errorf("expected timeout wording, got %q", text)
	}
}

func TestHandleOutputCapTruncation(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skipf("no bash available: %v", err)
	}
	h := newTestBashHandler(t)
	h.cfg.OutputCapChars = 100
	res, _, err := h.handle(context.Background(), nil, BashInput{Command: "yes hello | head -c 200"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "truncated:") {
		t.Errorf("expected truncation notice, got %q", text)
	}
}
