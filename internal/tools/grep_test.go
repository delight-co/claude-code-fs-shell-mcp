package tools

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestGrepHandler(t *testing.T) *grepHandler {
	t.Helper()
	return &grepHandler{
		cfg: GrepConfig{
			TimeoutMs:      20_000,
			OutputCapChars: 20_000,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func intPtr(i int) *int { return &i }

func TestPaginate(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name          string
		limit, offset int
		wantItems     []string
		wantApplied   *int
	}{
		{"unlimited", 0, 0, items, nil},
		{"full", 5, 0, items, nil},
		{"limit 2", 2, 0, []string{"a", "b"}, intPtr(2)},
		{"offset 2", 0, 2, []string{"c", "d", "e"}, nil},
		{"offset+limit", 1, 2, []string{"c"}, intPtr(1)},
		{"offset past end", 5, 10, []string{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, gotApplied := paginate(items, c.limit, c.offset)
			if len(got) != len(c.wantItems) {
				t.Errorf("len: got %d, want %d", len(got), len(c.wantItems))
			}
			for i := range got {
				if got[i] != c.wantItems[i] {
					t.Errorf("item %d: got %q, want %q", i, got[i], c.wantItems[i])
				}
			}
			if (gotApplied == nil) != (c.wantApplied == nil) {
				t.Errorf("appliedLimit nil mismatch: got %v, want %v", gotApplied, c.wantApplied)
			}
			if gotApplied != nil && c.wantApplied != nil && *gotApplied != *c.wantApplied {
				t.Errorf("appliedLimit: got %d, want %d", *gotApplied, *c.wantApplied)
			}
		})
	}
}

func TestBuildPaginationSuffix(t *testing.T) {
	limit := 250
	cases := []struct {
		name    string
		applied *int
		offset  int
		want    string
	}{
		{"none", nil, 0, ""},
		{"limit only", &limit, 0, "limit: 250"},
		{"offset only", nil, 10, "offset: 10"},
		{"both", &limit, 10, "limit: 250, offset: 10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildPaginationSuffix(c.applied, c.offset); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestPluraliseFile(t *testing.T) {
	if got := pluraliseFile(1); got != "file" {
		t.Errorf("singular: %q", got)
	}
	if got := pluraliseFile(2); got != "files" {
		t.Errorf("plural: %q", got)
	}
	if got := pluraliseFile(0); got != "files" {
		t.Errorf("zero: %q", got)
	}
}

func TestPluraliseOccurrence(t *testing.T) {
	if got := pluraliseOccurrence(1); got != "occurrence" {
		t.Errorf("singular: %q", got)
	}
	if got := pluraliseOccurrence(2); got != "occurrences" {
		t.Errorf("plural: %q", got)
	}
}

func TestRelativiseGrepLine(t *testing.T) {
	cwd, _ := os.Getwd()
	insidePath := filepath.Join(cwd, "foo.txt")
	line := insidePath + ":10:matched content"
	got := relativiseGrepLine(line)
	if !strings.HasPrefix(got, "foo.txt") {
		t.Errorf("got %q, expected to start with 'foo.txt'", got)
	}
	if !strings.Contains(got, ":10:matched content") {
		t.Errorf("rest not preserved: %q", got)
	}

	winLine := "C:/path/foo.txt:10:matched"
	got = relativiseGrepLine(winLine)
	if !strings.Contains(got, ":10:matched") {
		t.Errorf("windows drive line: got %q", got)
	}
}

func TestSortFilesByMTimeDesc(t *testing.T) {
	tmp := t.TempDir()
	p1 := filepath.Join(tmp, "a.txt")
	p2 := filepath.Join(tmp, "b.txt")
	p3 := filepath.Join(tmp, "c.txt")
	for _, p := range []string{p1, p2, p3} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// Set mtimes explicitly so the ordering is deterministic regardless
	// of filesystem mtime resolution.
	now := time.Now()
	if err := os.Chtimes(p1, now, now.Add(-2*time.Second)); err != nil {
		t.Fatalf("chtimes p1: %v", err)
	}
	if err := os.Chtimes(p2, now, now.Add(-1*time.Second)); err != nil {
		t.Fatalf("chtimes p2: %v", err)
	}
	if err := os.Chtimes(p3, now, now); err != nil {
		t.Fatalf("chtimes p3: %v", err)
	}
	paths := []string{p1, p2, p3}
	sortFilesByMTimeDesc(paths)
	if paths[0] != p3 {
		t.Errorf("expected p3 first (most recent), got %s", paths[0])
	}
	if paths[1] != p2 {
		t.Errorf("expected p2 second, got %s", paths[1])
	}
	if paths[2] != p1 {
		t.Errorf("expected p1 third, got %s", paths[2])
	}
}

func TestGrepBuildArgs_FilesWithMatches(t *testing.T) {
	h := newTestGrepHandler(t)
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp"})
	if !sliceContains(args, "--hidden") {
		t.Errorf("missing --hidden")
	}
	if !sliceContains(args, "-l") {
		t.Errorf("missing -l for files_with_matches mode")
	}
	if !sliceContains(args, "--max-columns") {
		t.Errorf("missing --max-columns")
	}
	if !sliceContainsConsecutive(args, "--glob", "!.git") {
		t.Errorf("missing VCS dir exclusion --glob !.git")
	}
	if args[len(args)-2] != "foo" {
		t.Errorf("pattern not in expected position; args: %v", args)
	}
	if args[len(args)-1] != "/tmp" {
		t.Errorf("search root not last; args: %v", args)
	}
}

func TestGrepBuildArgs_Content(t *testing.T) {
	h := newTestGrepHandler(t)
	tr := true
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp", OutputMode: "content", N: &tr, I: true})
	if !sliceContains(args, "-n") {
		t.Errorf("missing -n in content mode (default true)")
	}
	if !sliceContains(args, "-i") {
		t.Errorf("missing -i")
	}
	if sliceContains(args, "-l") {
		t.Errorf("should not have -l in content mode")
	}
}

func TestGrepBuildArgs_Count(t *testing.T) {
	h := newTestGrepHandler(t)
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp", OutputMode: "count"})
	if !sliceContains(args, "-c") {
		t.Errorf("missing -c in count mode")
	}
	if !sliceContains(args, "-H") {
		t.Errorf("missing -H in count mode")
	}
}

func TestGrepBuildArgs_ContextPrecedence(t *testing.T) {
	h := newTestGrepHandler(t)
	one := 1
	two := 2
	three := 3
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp", OutputMode: "content", Context: &three, C: &two, B: &one, A: &one})
	cidx := sliceIndexOf(args, "-C")
	if cidx < 0 || args[cidx+1] != "3" {
		t.Errorf("expected -C 3 from context; args: %v", args)
	}
}

func TestGrepBuildArgs_PatternLeadingDash(t *testing.T) {
	h := newTestGrepHandler(t)
	args := h.buildArgs(GrepInput{Pattern: "-foo", Path: "/tmp"})
	idx := sliceIndexOf(args, "-e")
	if idx < 0 || args[idx+1] != "-foo" {
		t.Errorf("expected -e -foo for leading-dash pattern; args: %v", args)
	}
}

func TestGrepBuildArgs_GlobBraceGroup(t *testing.T) {
	h := newTestGrepHandler(t)
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp", Glob: "*.{ts,tsx}"})
	if !sliceContainsConsecutive(args, "--glob", "*.{ts,tsx}") {
		t.Errorf("brace glob not preserved as single arg; args: %v", args)
	}
}

func TestGrepBuildArgs_GlobCommaSplit(t *testing.T) {
	h := newTestGrepHandler(t)
	args := h.buildArgs(GrepInput{Pattern: "foo", Path: "/tmp", Glob: "*.js,*.ts"})
	if !sliceContainsConsecutive(args, "--glob", "*.js") {
		t.Errorf("comma split missing *.js; args: %v", args)
	}
	if !sliceContainsConsecutive(args, "--glob", "*.ts") {
		t.Errorf("comma split missing *.ts; args: %v", args)
	}
}

func TestHandleEmptyPatternRejected(t *testing.T) {
	h := newTestGrepHandler(t)
	_, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: ""})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != errGrepPatternRequired {
		t.Errorf("wrong wording: %q", err.Error())
	}
}

func TestHandleNullByteRejected(t *testing.T) {
	h := newTestGrepHandler(t)
	_, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: "foo\x00bar"})
	if err == nil {
		t.Fatal("expected error for null byte")
	}
	if !strings.Contains(err.Error(), "Grep pattern cannot contain null bytes") {
		t.Errorf("wrong wording: %q", err.Error())
	}
}

func TestHandlePathNotFound(t *testing.T) {
	h := newTestGrepHandler(t)
	_, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: "foo", Path: "/nonexistent-path-xyz-ccfs"})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "Path does not exist:") {
		t.Errorf("wrong wording: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Note: your current working directory is") {
		t.Errorf("missing cwd hint: %q", err.Error())
	}
}

func TestHandleSimpleSearch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "foo.txt")
	if err := os.WriteFile(p, []byte("hello world\nfoo bar\nbaz\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGrepHandler(t)
	res, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: "foo", Path: tmp})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Found") || !strings.Contains(text, "foo.txt") {
		t.Errorf("expected 'Found ... foo.txt', got %q", text)
	}
}

func TestHandleNoMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "foo.txt")
	if err := os.WriteFile(p, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGrepHandler(t)
	res, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: "xxxxxNOTHINGxxxxx", Path: tmp})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if text != noticeGrepNoFilesFound {
		t.Errorf("expected %q, got %q", noticeGrepNoFilesFound, text)
	}
}

func TestHandleContentMode(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not available: %v", err)
	}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "foo.txt")
	if err := os.WriteFile(p, []byte("line one\nfoo here\nline three\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newTestGrepHandler(t)
	res, _, err := h.handle(context.Background(), nil, GrepInput{Pattern: "foo", Path: tmp, OutputMode: "content"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	text := extractText(t, res)
	if !strings.Contains(text, "foo here") {
		t.Errorf("content mode: expected 'foo here', got %q", text)
	}
}

// === slice helpers for arg-shape tests ===

func sliceContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func sliceContainsConsecutive(haystack []string, a, b string) bool {
	for i := 0; i < len(haystack)-1; i++ {
		if haystack[i] == a && haystack[i+1] == b {
			return true
		}
	}
	return false
}

func sliceIndexOf(haystack []string, needle string) int {
	for i, h := range haystack {
		if h == needle {
			return i
		}
	}
	return -1
}
