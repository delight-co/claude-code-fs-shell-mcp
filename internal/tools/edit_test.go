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
)

func newTestEditHandler(reg ReadStateAccess) *editHandler {
	return &editHandler{
		cfg:      DefaultEditConfig(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry: reg,
	}
}

func TestEdit_RelativePathRefused(t *testing.T) {
	t.Parallel()
	h := newTestEditHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  "relative/path.txt",
		OldString: "x",
		NewString: "y",
	})
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("want absolute-path error, got %v", err)
	}
}

func TestEdit_EmptyPathRefused(t *testing.T) {
	t.Parallel()
	h := newTestEditHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, EditInput{FilePath: ""})
	if err == nil || !strings.Contains(err.Error(), "file_path is required") {
		t.Fatalf("want required-path error, got %v", err)
	}
}

func TestEdit_NoOpRefused(t *testing.T) {
	t.Parallel()
	h := newTestEditHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  "/tmp/whatever.txt",
		OldString: "same",
		NewString: "same",
	})
	if err == nil || !strings.Contains(err.Error(), "No changes to make") {
		t.Fatalf("want no-op error, got %v", err)
	}
}

func TestEdit_FileTooLargeRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := &editHandler{
		cfg:      EditConfig{MaxFileSize: 8},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry: reg,
	}
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "0",
		NewString: "X",
	})
	if err == nil || !strings.Contains(err.Error(), "File is too large to edit") {
		t.Fatalf("want too-large error, got %v", err)
	}
}

func TestEdit_FileDoesNotExistRefused(t *testing.T) {
	t.Parallel()
	h := newTestEditHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  filepath.Join(t.TempDir(), "missing.txt"),
		OldString: "x",
		NewString: "y",
	})
	if err == nil || !strings.Contains(err.Error(), "File does not exist") {
		t.Fatalf("want does-not-exist error, got %v", err)
	}
}

func TestEdit_CreateNewFile(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "new.txt")
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "",
		NewString: "fresh\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "fresh\n" {
		t.Errorf("file content = %q, want %q", got, "fresh\n")
	}
}

func TestEdit_CreateAgainstExistingNonEmptyRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("already there\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "",
		NewString: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "Cannot create new file - file already exists") {
		t.Fatalf("want cannot-create error, got %v", err)
	}
}

func TestEdit_IpynbRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "notebook.ipynb")
	if err := os.WriteFile(path, []byte(`{"cells":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, []byte(`{"cells":[]}`))
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: `{"cells":[]}`,
		NewString: `{"cells":[1]}`,
	})
	if err == nil || !strings.Contains(err.Error(), "Jupyter Notebook") {
		t.Fatalf("want ipynb-reject error, got %v", err)
	}
}

func TestEdit_ExistingButUnreadRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "hello",
		NewString: "goodbye",
	})
	if err == nil || !strings.Contains(err.Error(), errExistingButUnread) {
		t.Fatalf("want existing-but-unread error, got %v", err)
	}
}

func TestEdit_SuccessSingleReplacement(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "ok.txt")
	original := []byte("the quick brown fox\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	res, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "brown",
		NewString: "red",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "the quick red fox\n" {
		t.Errorf("file content = %q, want %q", got, "the quick red fox\n")
	}
	if !strings.Contains(textOf(t, res), "has been updated successfully") {
		t.Errorf("unexpected success message: %q", textOf(t, res))
	}
}

func TestEdit_StringNotFound(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "miss.txt")
	original := []byte("hello world\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "missing",
		NewString: "replacement",
	})
	if err == nil || !strings.Contains(err.Error(), "String to replace not found in file") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestEdit_MultipleMatchesWithoutReplaceAllRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "dup.txt")
	original := []byte("aaa\naaa\naaa\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "aaa",
		NewString: "bbb",
	})
	if err == nil || !strings.Contains(err.Error(), "Found 3 matches") {
		t.Fatalf("want multiple-matches error, got %v", err)
	}
}

func TestEdit_ReplaceAllWorks(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "all.txt")
	original := []byte("aaa\naaa\naaa\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	res, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:   path,
		OldString:  "aaa",
		NewString:  "bbb",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "bbb\nbbb\nbbb\n" {
		t.Errorf("file content = %q, want bbb x3", got)
	}
	if !strings.Contains(textOf(t, res), "All occurrences were successfully replaced") {
		t.Errorf("unexpected success message: %q", textOf(t, res))
	}
}

func TestEdit_DeletionAbsorbsTrailingNewline(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "del.txt")
	original := []byte("keep\nremove\nkeep\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "remove",
		NewString: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "keep\nkeep\n" {
		t.Errorf("file content = %q, want %q (trailing newline absorbed)", got, "keep\nkeep\n")
	}
}

func TestEdit_UnicodeEscapeFallback(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "uni.txt")
	// File contains real é (UTF-8 0xC3 0xA9), but old_string is the
	// six-character literal é. Strategy 3 should kick in.
	original := []byte("\xc3\xa9 au lait\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	sixCharE := string([]byte{'\\', 'u', '0', '0', 'e', '9'})
	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: sixCharE, // six-character literal `é`
		NewString: "X",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "X au lait\n" {
		t.Errorf("file content = %q, want %q", got, "X au lait\n")
	}
}

func TestEdit_StringNotFoundAppendsUnicodeNoteForEscape(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "miss.txt")
	original := []byte("plain ascii content\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: `doesénot_exist`,
		NewString: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "swapping \\uXXXX escapes") {
		t.Fatalf("want unicode-note suffix in not-found error, got %v", err)
	}
}

func TestEdit_ModifiedSinceFullReadRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	path := filepath.Join(t.TempDir(), "mod.txt")
	original := []byte("original\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	if err := os.WriteFile(path, []byte("MUTATED bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  path,
		OldString: "MUTATED",
		NewString: "x",
	})
	if err == nil || !strings.Contains(err.Error(), errModifiedPreFlight) {
		t.Fatalf("want modified-since-read pre-flight error, got %v", err)
	}
}

func TestEdit_SymlinkRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestEditHandler(reg)
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(target, []byte("real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", target, []byte("real\n"))

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}
	seedFullRead(t, reg, "", link, []byte("real\n"))

	_, _, err := h.handle(context.Background(), nil, EditInput{
		FilePath:  link,
		OldString: "real",
		NewString: "fake",
	})
	if err == nil || !strings.Contains(err.Error(), "Refusing to write through symlink") {
		t.Fatalf("want symlink-refused error, got %v", err)
	}
}

func TestFindMatch_ExactSubstring(t *testing.T) {
	t.Parallel()
	got := findMatch("hello world", "world")
	if got.Actual != "world" {
		t.Errorf("Actual = %q, want world", got.Actual)
	}
	if got.DidEscapeFallback {
		t.Errorf("should not have used escape fallback")
	}
}

func TestFindMatch_EscapeFallback(t *testing.T) {
	t.Parallel()
	// File contains real é (UTF-8 0xC3 0xA9). old_string is the
	// six-character literal escape sequence `é`. Strategy 3
	// converts and matches.
	sixCharE := string([]byte{'\\', 'u', '0', '0', 'e', '9'})
	got := findMatch("\xc3\xa9", sixCharE)
	if got.Actual != "\xc3\xa9" {
		t.Errorf("Actual = %q, want UTF-8 é", got.Actual)
	}
	if !got.DidEscapeFallback {
		t.Errorf("should have used escape fallback")
	}
}

func TestFindMatch_NoMatch(t *testing.T) {
	t.Parallel()
	got := findMatch("hello world", "missing")
	if got.Actual != "" {
		t.Errorf("Actual = %q, want empty", got.Actual)
	}
}

func TestConvertEscapeToChar(t *testing.T) {
	t.Parallel()
	// Compose literal \uXXXX byte sequences from raw bytes to avoid the
	// JSON / tool-call escape gymnastics involved in writing them as
	// string literals in this source file.
	e00e9 := string([]byte{'\\', 'u', '0', '0', 'e', '9'})                                   // 6 chars
	zhongwen := string([]byte{'\\', 'u', '4', 'e', '2', 'd', '\\', 'u', '6', '5', '8', '7'}) // 12 chars
	doubleBackslashEscape := string([]byte{'\\', '\\', 'u', '0', '0', 'e', '9'})             // \\u00e9: escaped backslash preserved
	mixed := "mixed " + e00e9 + " text"

	cases := []struct {
		name       string
		in         string
		wantOut    string
		wantDidCvt bool
	}{
		{"no escape", "no escape", "no escape", false},
		{"single escape", e00e9, "\xc3\xa9", true},
		{"two escapes", zhongwen, "\xe4\xb8\xad\xe6\x96\x87", true},
		{"double backslash preserved", doubleBackslashEscape, doubleBackslashEscape, false},
		{"mixed escape and ascii", mixed, "mixed \xc3\xa9 text", true},
	}
	for _, c := range cases {
		out, did := convertEscapeToChar(c.in)
		if out != c.wantOut {
			t.Errorf("%s: convertEscapeToChar(%q) out = %q, want %q", c.name, c.in, out, c.wantOut)
		}
		if did != c.wantDidCvt {
			t.Errorf("%s: convertEscapeToChar(%q) didConvert = %v, want %v", c.name, c.in, did, c.wantDidCvt)
		}
	}
}

func TestFormatBytesBinary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size int64
		want string
	}{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1KB"},
		{1024 * 1024, "1MB"},
		{1024 * 1024 * 1024, "1GB"},
		{2 * 1024 * 1024 * 1024, "2GB"},
	}
	for _, c := range cases {
		if got := formatBytesBinary(c.size); got != c.want {
			t.Errorf("formatBytesBinary(%d) = %q, want %q", c.size, got, c.want)
		}
	}
}
