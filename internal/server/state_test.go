package server

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/delight-co/claude-code-fs-shell-mcp/internal/tools"
)

func newTestRegistry(t *testing.T, maxEntries int, ttl time.Duration) *ReadTrackingRegistry {
	t.Helper()
	return NewReadTrackingRegistry(maxEntries, ttl, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestReadTrackingRegistry_GetMissingSession(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	if _, ok := r.Get("missing-session", "/path"); ok {
		t.Fatalf("expected miss for unknown session")
	}
}

func TestReadTrackingRegistry_GetMissingPath(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	r.Seed("session-a", "/path1", tools.ReadEntry{ContentHash: "h1"})
	if _, ok := r.Get("session-a", "/path2"); ok {
		t.Fatalf("expected miss for unknown path")
	}
}

func TestReadTrackingRegistry_SeedThenGet(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	want := tools.ReadEntry{
		Content:       []byte("hello"),
		ContentHash:   "abc",
		ModTimeMillis: 12345,
		Offset:        1,
		Limit:         10,
	}
	r.Seed("session-a", "/path", want)
	got, ok := r.Get("session-a", "/path")
	if !ok {
		t.Fatalf("expected hit")
	}
	if got.ContentHash != want.ContentHash {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, want.ContentHash)
	}
	if got.ModTimeMillis != want.ModTimeMillis {
		t.Errorf("ModTimeMillis = %d, want %d", got.ModTimeMillis, want.ModTimeMillis)
	}
	if got.Offset != want.Offset {
		t.Errorf("Offset = %d, want %d", got.Offset, want.Offset)
	}
}

func TestReadTrackingRegistry_SeedEmptySessionIDIsNoop(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	r.Seed("", "/path", tools.ReadEntry{ContentHash: "h"})
	if _, ok := r.Get("", "/path"); ok {
		t.Fatalf("empty session id must not be tracked")
	}
}

func TestReadTrackingRegistry_LRUEvictsOldest(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 2, time.Minute)
	r.Seed("s", "/a", tools.ReadEntry{ContentHash: "a"})
	r.Seed("s", "/b", tools.ReadEntry{ContentHash: "b"})
	r.Seed("s", "/c", tools.ReadEntry{ContentHash: "c"}) // evicts /a (oldest, untouched)
	if _, ok := r.Get("s", "/a"); ok {
		t.Errorf("/a should have been evicted")
	}
	if _, ok := r.Get("s", "/b"); !ok {
		t.Errorf("/b should remain")
	}
	if _, ok := r.Get("s", "/c"); !ok {
		t.Errorf("/c should remain")
	}
}

func TestReadTrackingRegistry_SessionsIsolated(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	r.Seed("s1", "/p", tools.ReadEntry{ContentHash: "v1"})
	r.Seed("s2", "/p", tools.ReadEntry{ContentHash: "v2"})
	if got, _ := r.Get("s1", "/p"); got.ContentHash != "v1" {
		t.Errorf("s1: hash = %q, want v1", got.ContentHash)
	}
	if got, _ := r.Get("s2", "/p"); got.ContentHash != "v2" {
		t.Errorf("s2: hash = %q, want v2", got.ContentHash)
	}
}

func TestReadTrackingRegistry_TTLCleansUpAfterIdle(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, 30*time.Millisecond)
	r.Seed("s", "/p", tools.ReadEntry{ContentHash: "v"})
	time.Sleep(120 * time.Millisecond)
	if _, ok := r.Get("s", "/p"); ok {
		t.Errorf("entry should have been cleaned up after TTL")
	}
}

func TestReadTrackingRegistry_AccessResetsTTL(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, 60*time.Millisecond)
	r.Seed("s", "/p", tools.ReadEntry{ContentHash: "v"})
	for i := 0; i < 4; i++ {
		time.Sleep(25 * time.Millisecond)
		if _, ok := r.Get("s", "/p"); !ok {
			t.Fatalf("iteration %d: expected hit after access reset, got miss", i)
		}
	}
}

func TestReadTrackingRegistry_LockPathSerialisesSamePath(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	var counter int
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := r.LockPath("/p")
			defer unlock()
			counter++
		}()
	}
	wg.Wait()
	if counter != 200 {
		t.Errorf("counter = %d, want 200 (race detected)", counter)
	}
}

func TestReadTrackingRegistry_LockPathDifferentPathsDoNotBlock(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t, 10, time.Minute)
	unlock1 := r.LockPath("/a")
	defer unlock1()
	done := make(chan struct{})
	go func() {
		unlock2 := r.LockPath("/b")
		defer unlock2()
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(200 * time.Millisecond):
		t.Fatal("LockPath blocked on a different path")
	}
}
