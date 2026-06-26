package server

import (
	"io"
	"log/slog"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/delight-co/claude-code-fs-shell-mcp/internal/tools"
)

// ReadTrackingRegistry stores per-MCP-session read tracking state.
//
// It is consulted by tools that need to know whether a given file has
// been read in the current session (and what the file looked like at
// read time). Session state is held for an idle TTL grace period after
// the last access, so transient transport drops do not lose the
// read-tracking entries.
//
// The registry is safe for concurrent use.
type ReadTrackingRegistry struct {
	maxEntriesPerSession int
	sessionTTL           time.Duration
	logger               *slog.Logger
	clock                func() time.Time

	mu       sync.Mutex
	sessions map[string]*sessionEntry

	// pathMutexes serialises concurrent writes that target the same
	// absolute path. A mutex is shared across sessions because the
	// underlying file is shared at the OS level.
	pathMutexes sync.Map // string → *sync.Mutex
}

type sessionEntry struct {
	cache    *lru.Cache[string, tools.ReadEntry]
	lastUsed time.Time
	timer    *time.Timer
}

// NewReadTrackingRegistry returns an initialised registry.
//
// maxEntriesPerSession bounds the number of distinct paths held per
// session; once exceeded, the least-recently-used entry is evicted.
// sessionTTL is the idle grace period: a session that has not been
// touched (Get or Seed) for this long is cleaned up entirely.
func NewReadTrackingRegistry(maxEntriesPerSession int, sessionTTL time.Duration, logger *slog.Logger) *ReadTrackingRegistry {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ReadTrackingRegistry{
		maxEntriesPerSession: maxEntriesPerSession,
		sessionTTL:           sessionTTL,
		logger:               logger,
		clock:                time.Now,
		sessions:             make(map[string]*sessionEntry),
	}
}

// Get returns the cached read entry for (sessionID, path), or the zero
// value and false if none exists. Accessing an entry refreshes the
// session's idle TTL.
func (r *ReadTrackingRegistry) Get(sessionID, path string) (tools.ReadEntry, bool) {
	if sessionID == "" {
		return tools.ReadEntry{}, false
	}
	r.mu.Lock()
	s, ok := r.sessions[sessionID]
	if ok {
		s.lastUsed = r.clock()
		r.resetTimerLocked(sessionID, s)
	}
	r.mu.Unlock()
	if !ok {
		return tools.ReadEntry{}, false
	}
	return s.cache.Get(path)
}

// Seed records (or refreshes) the read entry for (sessionID, path).
// Creates the session entry if it does not already exist. A zero
// sessionID is a no-op so callers can pass through tools that have not
// negotiated a session id yet.
func (r *ReadTrackingRegistry) Seed(sessionID, path string, entry tools.ReadEntry) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	s, ok := r.sessions[sessionID]
	if !ok {
		cache, err := lru.New[string, tools.ReadEntry](r.maxEntriesPerSession)
		if err != nil {
			r.mu.Unlock()
			r.logger.Error("read tracking: failed to initialise LRU cache",
				"err", err,
				"session_id", sessionID,
			)
			return
		}
		s = &sessionEntry{cache: cache, lastUsed: r.clock()}
		r.sessions[sessionID] = s
	} else {
		s.lastUsed = r.clock()
	}
	r.resetTimerLocked(sessionID, s)
	r.mu.Unlock()
	s.cache.Add(path, entry)
}

// LockPath returns an unlock function. Each absolute path has a single
// mutex shared across sessions, so concurrent writes targeting the same
// path serialise at the OS level even when they originate from
// different sessions.
func (r *ReadTrackingRegistry) LockPath(path string) func() {
	m, _ := r.pathMutexes.LoadOrStore(path, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// resetTimerLocked stops any pending TTL cleanup for the session and
// installs a fresh one. r.mu must be held by the caller.
func (r *ReadTrackingRegistry) resetTimerLocked(sessionID string, s *sessionEntry) {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(r.sessionTTL, func() {
		r.cleanupExpiredSession(sessionID)
	})
}

func (r *ReadTrackingRegistry) cleanupExpiredSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return
	}
	if r.clock().Sub(s.lastUsed) < r.sessionTTL {
		// Touched in the meantime; the next timer fire will catch up.
		return
	}
	delete(r.sessions, sessionID)
	r.logger.Info("read tracking: session cleaned up after idle TTL",
		"session_id", sessionID,
		"idle_seconds", r.sessionTTL.Seconds(),
	)
}
