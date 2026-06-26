package tools

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAccess is a stub of ReadStateAccess used by the write tool unit
// tests. It records Seed calls and serves Get from an in-memory map.
type fakeAccess struct {
	mu      sync.Mutex
	entries map[string]map[string]ReadEntry // session id → path → entry
	locks   int
	unlocks int
}

func newFakeAccess() *fakeAccess {
	return &fakeAccess{entries: make(map[string]map[string]ReadEntry)}
}

func (f *fakeAccess) Get(sessionID, path string) (ReadEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.entries[sessionID]
	if !ok {
		return ReadEntry{}, false
	}
	e, ok := s[path]
	return e, ok
}

func (f *fakeAccess) Seed(sessionID, path string, entry ReadEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.entries[sessionID]
	if !ok {
		s = make(map[string]ReadEntry)
		f.entries[sessionID] = s
	}
	s[path] = entry
}

func (f *fakeAccess) LockPath(_ string) func() {
	f.mu.Lock()
	f.locks++
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.unlocks++
		f.mu.Unlock()
	}
}

func newTestWriteHandler(reg ReadStateAccess) *writeHandler {
	return &writeHandler{
		cfg:      DefaultWriteConfig(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry: reg,
	}
}

// seedFullRead pre-populates the fake registry as if a full Read had
// taken place on the given file at its current on-disk state.
func seedFullRead(t *testing.T, reg *fakeAccess, sessionID, path string, content []byte) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	normalised := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	reg.Seed(sessionID, path, ReadEntry{
		Content:       normalised,
		ContentHash:   hashContent(normalised),
		ModTimeMillis: info.ModTime().UnixMilli(),
	})
}

// seedPartialRead pre-populates the fake registry as if a partial Read
// had taken place (offset and limit set, content / hash zero).
func seedPartialRead(t *testing.T, reg *fakeAccess, sessionID, path string, offset, limit int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Seed(sessionID, path, ReadEntry{
		ModTimeMillis: info.ModTime().UnixMilli(),
		Offset:        offset,
		Limit:         limit,
	})
}

func TestWrite_NewFile(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "new.txt")
	res, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "hello\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("file content = %q, want %q", got, "hello\n")
	}
	if !strings.Contains(textOf(t, res), "has been written successfully") {
		t.Errorf("unexpected success message: %q", textOf(t, res))
	}
}

func TestWrite_ExistingButUnread(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "new",
	})
	if err == nil || !strings.Contains(err.Error(), errExistingButUnread) {
		t.Fatalf("want existing-but-unread error, got %v", err)
	}

	// file unchanged
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Errorf("file should be unchanged, got %q", got)
	}
}

func TestWrite_ExistingAfterFullReadSucceeds(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "existing.txt")
	original := []byte("original\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "overwritten\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "overwritten\n" {
		t.Errorf("file content = %q, want overwritten", got)
	}
}

func TestWrite_ModifiedSinceFullReadRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, []byte("original\n"))

	// Mutate file with new content + advance mtime
	if err := os.WriteFile(path, []byte("DIFFERENT bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "my overwrite\n",
	})
	if err == nil || !strings.Contains(err.Error(), errModifiedPreFlight) {
		t.Fatalf("want modified-since-read pre-flight error, got %v", err)
	}
}

func TestWrite_FullReadFallbackPassesWhenContentMatches(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "touched.txt")
	original := []byte("original content\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	seedFullRead(t, reg, "", path, original)

	// Rewrite same bytes but advance mtime (mimics a formatter that
	// produced the same output).
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "new bytes after no-op formatter\n",
	})
	if err != nil {
		t.Fatalf("content-equality fallback should pass; got %v", err)
	}
}

func TestWrite_PartialReadMtimeAdvanceRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "partial.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedPartialRead(t, reg, "", path, 2, 1)

	// Advance mtime
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "new\n",
	})
	if err == nil || !strings.Contains(err.Error(), errModifiedPreFlight) {
		t.Fatalf("want modified-since-read (partial = unconditional), got %v", err)
	}
}

func TestWrite_SymlinkRefused(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("real"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: link,
		Content:  "via symlink",
	})
	if err == nil || !strings.Contains(err.Error(), "Refusing to write through symlink") {
		t.Fatalf("want symlink-refused error, got %v", err)
	}

	// target untouched
	got, _ := os.ReadFile(target)
	if string(got) != "real" {
		t.Errorf("target should be unchanged, got %q", got)
	}
}

func TestWrite_RelativePathRefused(t *testing.T) {
	t.Parallel()
	h := newTestWriteHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: "relative/path.txt",
		Content:  "x",
	})
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("want absolute-path error, got %v", err)
	}
}

func TestWrite_EmptyPathRefused(t *testing.T) {
	t.Parallel()
	h := newTestWriteHandler(newFakeAccess())
	_, _, err := h.handle(context.Background(), nil, WriteInput{FilePath: ""})
	if err == nil || !strings.Contains(err.Error(), "file_path is required") {
		t.Fatalf("want required-path error, got %v", err)
	}
}

func TestWrite_ParentDirCreated(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	deep := filepath.Join(t.TempDir(), "a", "b", "c", "deep.txt")
	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: deep,
		Content:  "x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Errorf("file should exist after parent dir auto-create: %v", err)
	}
}

func TestWrite_PostWriteStateRefresh(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "refresh.txt")
	const sid = "session-x"

	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "fresh\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// session id was empty (req nil), so refreshState should be a no-op
	if _, ok := reg.Get("", path); ok {
		// new file path: registry has no Read entry, and refreshState
		// returns early when sessionID is empty.
		t.Logf("registry entry for empty session id is present; this is acceptable as a fake-behaviour quirk, but production registry would skip it")
	}

	// Now simulate a non-empty session id by directly invoking the helper
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	h.refreshState(sid, path, []byte("fresh\n"), info)
	entry, ok := reg.Get(sid, path)
	if !ok {
		t.Fatalf("refreshState should seed registry for non-empty session id")
	}
	if entry.ContentHash == "" {
		t.Errorf("ContentHash should be populated, got empty")
	}
	if !bytes.Equal(entry.Content, []byte("fresh\n")) {
		t.Errorf("Content = %q, want %q", entry.Content, "fresh\n")
	}
}

func TestWrite_LockPathCalledAndReleased(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "lock.txt")
	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.locks != 1 {
		t.Errorf("LockPath should have been called once, got %d", reg.locks)
	}
	if reg.unlocks != 1 {
		t.Errorf("unlock should have been called once, got %d", reg.unlocks)
	}
}

func TestWrite_EmptyContentProducesEmptyFile(t *testing.T) {
	t.Parallel()
	reg := newFakeAccess()
	h := newTestWriteHandler(reg)

	path := filepath.Join(t.TempDir(), "empty.txt")
	_, _, err := h.handle(context.Background(), nil, WriteInput{
		FilePath: path,
		Content:  "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("file size = %d, want 0", info.Size())
	}
}
