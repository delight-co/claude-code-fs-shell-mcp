package tools

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newTestGlobHandler(t *testing.T) *globHandler {
	t.Helper()
	return &globHandler{
		cfg: GlobConfig{
			TimeoutMs:      20_000,
			OutputCapChars: 50_000,
			MaxResults:     100,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestEnvTruthy(t *testing.T) {
	cases := []struct {
		in      string
		def     bool
		want    bool
		comment string
	}{
		{"", true, true, "empty falls back to default true"},
		{"", false, false, "empty falls back to default false"},
		{"1", false, true, "1 truthy"},
		{"true", false, true, "true truthy"},
		{"TRUE", false, true, "case-insensitive"},
		{"yes", false, true, "yes truthy"},
		{"on", false, true, "on truthy"},
		{"0", true, false, "0 not truthy"},
		{"false", true, false, "false not truthy"},
		{"no", true, false, "no not truthy"},
	}
	for _, c := range cases {
		t.Run(c.comment, func(t *testing.T) {
			if got := envTruthy(c.in, c.def); got != c.want {
				t.Errorf("envTruthy(%q, %v) = %v, want %v", c.in, c.def, got, c.want)
			}
		})
	}
}

func TestSplitAbsolutePattern(t *testing.T) {
	cases := []struct {
		pattern, wantBase, wantRel string
	}{
		{"/foo/bar/file.go", "/foo/bar", "file.go"},
		{"/foo/bar/**/*.go", "/foo/bar", "**/*.go"},
		{"/*.go", "/", "*.go"},
		{"**/*.go", "", "**/*.go"},
		{"/foo/*.{ts,tsx}", "/foo", "*.{ts,tsx}"},
	}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			gotBase, gotRel := splitAbsolutePattern(c.pattern)
			if gotBase != c.wantBase || gotRel != c.wantRel {
				t.Errorf("splitAbsolutePattern(%q) = (%q, %q), want (%q, %q)",
					c.pattern, gotBase, gotRel, c.wantBase, c.wantRel)
			}
		})
	}
}

func TestGlobBuildArgs_Default(t *testing.T) {
	t.Setenv("CLAUDE_CODE_GLOB_NO_IGNORE", "")
	t.Setenv("CLAUDE_CODE_GLOB_HIDDEN", "")

	h := newTestGlobHandler(t)
	args := h.buildArgs("*.go")
	want := []string{"--files", "--glob", "*.go", "--sort=modified", "--no-ignore", "--hidden"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestGlobBuildArgs_NoIgnoreOff(t *testing.T) {
	t.Setenv("CLAUDE_CODE_GLOB_NO_IGNORE", "0")
	t.Setenv("CLAUDE_CODE_GLOB_HIDDEN", "true")

	h := newTestGlobHandler(t)
	args := h.buildArgs("*.go")
	for _, a := range args {
		if a == "--no-ignore" {
			t.Errorf("--no-ignore should be absent when env disables it")
		}
	}
	if !sliceContains(args, "--hidden") {
		t.Errorf("--hidden should be present")
	}
}

func TestGlobBuildArgs_HiddenOff(t *testing.T) {
	t.Setenv("CLAUDE_CODE_GLOB_NO_IGNORE", "true")
	t.Setenv("CLAUDE_CODE_GLOB_HIDDEN", "0")

	h := newTestGlobHandler(t)
	args := h.buildArgs("*.go")
	if sliceContains(args, "--hidden") {
		t.Errorf("--hidden should be absent when env disables it")
	}
	if !sliceContains(args, "--no-ignore") {
		t.Errorf("--no-ignore should be present")
	}
}

func TestGlobFormatResult(t *testing.T) {
	h := newTestGlobHandler(t)

	// empty
	if got := h.formatResult(nil, false, 0, 0); got != noticeGlobNoFilesFound {
		t.Errorf("empty: got %q, want %q", got, noticeGlobNoFilesFound)
	}

	// non-truncated
	got := h.formatResult([]string{"a.go", "b.go"}, false, 2, 2)
	if got != "a.go\nb.go" {
		t.Errorf("non-truncated: got %q", got)
	}

	// truncated
	got = h.formatResult([]string{"a.go", "b.go"}, true, 2, 10)
	wantPrefix := "a.go\nb.go\n(Showing 2 of 10 matching files; 8 more are not listed."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("truncated: got %q, want prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, "Narrow the pattern or path to see the rest.)") {
		t.Errorf("truncated: missing trailing wording; got %q", got)
	}
}

func TestGlobResolveSearchRoot_RelPath(t *testing.T) {
	cwd, _ := os.Getwd()
	h := newTestGlobHandler(t)

	root, pat := h.resolveSearchRoot(GlobInput{Pattern: "**/*.go", Path: ""})
	if root != cwd {
		t.Errorf("empty path: root should be cwd %q, got %q", cwd, root)
	}
	if pat != "**/*.go" {
		t.Errorf("empty path: pattern unchanged, got %q", pat)
	}

	root, pat = h.resolveSearchRoot(GlobInput{Pattern: "**/*.go", Path: "/some/dir"})
	if root != "/some/dir" {
		t.Errorf("rel path: root should be /some/dir, got %q", root)
	}
	if pat != "**/*.go" {
		t.Errorf("rel path: pattern unchanged, got %q", pat)
	}
}

func TestGlobResolveSearchRoot_AbsolutePattern(t *testing.T) {
	h := newTestGlobHandler(t)
	// Absolute pattern silently overrides the caller-supplied path.
	root, pat := h.resolveSearchRoot(GlobInput{Pattern: "/foo/bar/**/*.go", Path: "/ignored"})
	if root != "/foo/bar" {
		t.Errorf("absolute pattern: root should be /foo/bar, got %q", root)
	}
	if pat != "**/*.go" {
		t.Errorf("absolute pattern: pattern should be **/*.go, got %q", pat)
	}
}

func TestGlobValidateEmptyPatternRejected(t *testing.T) {
	h := newTestGlobHandler(t)
	_, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: ""})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != errGlobPatternRequired {
		t.Errorf("wrong wording: %q", err.Error())
	}
}

func TestGlobValidateDirNotExist(t *testing.T) {
	h := newTestGlobHandler(t)
	_, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: "*.go", Path: "/nonexistent-glob-dir-xyz"})
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
	if !strings.Contains(err.Error(), "Directory does not exist:") {
		t.Errorf("wrong wording: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Note: your current working directory is") {
		t.Errorf("missing cwd hint: %q", err.Error())
	}
}

func TestGlobValidatePathNotDirectory(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "afile.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGlobHandler(t)
	_, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: "*.go", Path: filePath})
	if err == nil {
		t.Fatal("expected error for file path")
	}
	if !strings.Contains(err.Error(), "Path is not a directory:") {
		t.Errorf("wrong wording: %q", err.Error())
	}
}

func TestGlobSimpleSearch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "foo.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "bar.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGlobHandler(t)
	res, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: "*.go", Path: tmp})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "foo.go") {
		t.Errorf("expected foo.go in result, got %q", text)
	}
	if strings.Contains(text, "bar.txt") {
		t.Errorf("bar.txt should not match *.go, got %q", text)
	}
}

func TestGlobNoMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "foo.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGlobHandler(t)
	res, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: "*.go", Path: tmp})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if text != noticeGlobNoFilesFound {
		t.Errorf("expected %q, got %q", noticeGlobNoFilesFound, text)
	}
}

func TestGlobTruncationCap(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	for i := 0; i < 5; i++ {
		p := filepath.Join(tmp, "f"+strconv.Itoa(i)+".go")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	h := newTestGlobHandler(t)
	h.cfg.MaxResults = 3
	res, _, err := h.handle(context.Background(), nil, GlobInput{Pattern: "*.go", Path: tmp})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "(Showing 3 of 5 matching files; 2 more are not listed.") {
		t.Errorf("expected truncation notice for 3 of 5, got %q", text)
	}
}

// equalSlices is a small helper for the buildArgs tests.
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
