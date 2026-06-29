package tools

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestResolveRipgrep_Success verifies that the resolver returns a path
// that exists, is executable, and answers to `rg --version`. It works
// against either the embedded binary (the production path on supported
// platforms) or a host `rg` on PATH (the fallback path). When neither
// is available the test reports a skip rather than a failure: the
// resolver itself returning ErrRipgrepNotFound is a separate
// contract that does not need exercising on every host.
func TestResolveRipgrep_Success(t *testing.T) {
	path, cleanup, err := ResolveRipgrep()
	defer cleanup()

	if errors.Is(err, ErrRipgrepNotFound) {
		t.Skipf("no ripgrep available on this build: %v", err)
	}
	if err != nil {
		t.Fatalf("ResolveRipgrep: %v", err)
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat %s: %v", path, statErr)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("expected %s to be executable, got mode %v", path, info.Mode())
	}

	out, err := exec.CommandContext(t.Context(), path, "--version").Output()
	if err != nil {
		t.Fatalf("rg --version failed: %v (output: %q)", err, out)
	}
	if !strings.HasPrefix(string(out), "ripgrep ") {
		t.Errorf("rg --version output should start with 'ripgrep ', got: %q", out)
	}
}

// TestResolveRipgrep_CleanupIdempotent verifies that the cleanup
// function returned by ResolveRipgrep is safe to call twice and (when
// extraction took place) actually removes the temp file.
func TestResolveRipgrep_CleanupIdempotent(t *testing.T) {
	path, cleanup, err := ResolveRipgrep()
	if errors.Is(err, ErrRipgrepNotFound) {
		t.Skip("no ripgrep available on this build")
	}
	if err != nil {
		t.Fatalf("ResolveRipgrep: %v", err)
	}

	isExtracted := strings.HasPrefix(path, os.TempDir())

	cleanup()
	cleanup() // must be a no-op the second time around

	if isExtracted {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("expected extracted %s to be removed, got stat err: %v", path, statErr)
		}
	}
}
