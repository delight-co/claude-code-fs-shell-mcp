package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// ErrRipgrepNotFound is the resolver's failure case. The wording matches
// the ENOENT branch in docs/spec/grep.md and docs/spec/glob.md so that
// callers can pattern-match on it.
var ErrRipgrepNotFound = errors.New( //nolint:staticcheck // spec-pinned wording ends with period.
	"ripgrep not found on PATH. Install it (brew install ripgrep / apt install ripgrep / winget install BurntSushi.ripgrep.MSVC) or use the native claude binary which embeds it.",
)

// ResolveRipgrep returns a usable rg path and a cleanup function that
// removes any temporary extraction the resolver may have created. The
// resolution order is:
//
//  1. If a binary for the current platform was embedded at build time,
//     extract it to a temporary file and return that path.
//  2. Otherwise, look up `rg` on PATH and return its absolute path with
//     a no-op cleanup.
//  3. If neither resolves, return ErrRipgrepNotFound (and a no-op
//     cleanup so the caller can defer it unconditionally).
//
// The cleanup function is safe to call multiple times. The caller is
// expected to run it at server shutdown so the extracted binary does
// not linger in the OS temp directory.
func ResolveRipgrep() (path string, cleanup func(), err error) {
	if data := embeddedRipgrepBytes(); len(data) > 0 {
		extracted, rm, extractErr := extractEmbeddedRipgrep(data)
		if extractErr == nil {
			return extracted, rm, nil
		}
		// Fall through to PATH lookup if extraction fails (rare: disk
		// full, EROFS, ...) so the server is still usable in degraded
		// conditions. The caller's logger surfaces the underlying error
		// if it cares; we keep the resolver itself silent.
	}
	if p, lookErr := exec.LookPath("rg"); lookErr == nil {
		return p, func() {}, nil
	}
	return "", func() {}, ErrRipgrepNotFound
}

func extractEmbeddedRipgrep(data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "ccfs-rg-*"+rgBinaryExt)
	if err != nil {
		return "", nil, fmt.Errorf("create rg temp file: %w", err)
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("write rg temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("close rg temp file: %w", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("chmod rg temp file: %w", err)
	}
	removed := false
	cleanup := func() {
		if removed {
			return
		}
		removed = true
		_ = os.Remove(path)
	}
	return path, cleanup, nil
}
