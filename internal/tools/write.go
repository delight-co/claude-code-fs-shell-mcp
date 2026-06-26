package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WriteInput is the JSON input schema for the write tool.
type WriteInput struct {
	FilePath string `json:"file_path" jsonschema:"absolute path to the file to write; parent directories are created if missing"`
	Content  string `json:"content" jsonschema:"the exact bytes to write to the file; replaces the file entirely"`
}

// WriteConfig configures the write tool's tunable behaviour. Reserved
// for future use.
type WriteConfig struct{}

// DefaultWriteConfig returns the default write tool configuration.
func DefaultWriteConfig() WriteConfig { return WriteConfig{} }

const writeDescription = `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the Read tool first to read the file's contents. This tool will fail if you did not read the file first.
- Prefer the Edit tool for modifying existing files — it only sends the diff. Only use this tool to create new files or for complete rewrites.
- NEVER create documentation files (*.md) or README files unless explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.`

// Error string literals reproduced verbatim from docs/spec/write.md so
// callers can pattern-match on them.
const (
	errExistingButUnread    = "File has not been read yet. Read it first before writing to it."
	errModifiedPreFlight    = "File has been modified since read, either by the user or by a linter. Read it again before attempting to write it."
	errModifiedInCallTOCTOU = "File content has changed since it was last read. This commonly happens when a linter or formatter run via Bash rewrites the file. Call Read on this file to refresh, then retry the edit."
	errSymlinkRefusedFormat = "Refusing to write through symlink: %s. Resolve the symlink and pass the real target path explicitly."
)

// RegisterWrite adds the write tool to the given MCP server.
//
// The handler uses `any` as its output type so the SDK does not populate
// the response's structuredContent field. All payload is returned via
// Content blocks only, matching the project-wide response transport
// convention.
//
// The registry parameter is required: the read-before-overwrite and
// modified-since-read contracts depend on the per-session read-tracking
// state seeded by the Read tool.
func RegisterWrite(s *mcp.Server, cfg WriteConfig, logger *slog.Logger, registry ReadStateAccess) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &writeHandler{cfg: cfg, logger: logger, registry: registry}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "write",
		Description: writeDescription,
	}, h.handle)
}

type writeHandler struct {
	cfg      WriteConfig
	logger   *slog.Logger
	registry ReadStateAccess
}

func (h *writeHandler) handle(_ context.Context, req *mcp.CallToolRequest, in WriteInput) (*mcp.CallToolResult, any, error) {
	if in.FilePath == "" {
		return nil, nil, errors.New("file_path is required")
	}
	if !filepath.IsAbs(in.FilePath) {
		return nil, nil, fmt.Errorf("file_path must be an absolute path, not relative: %s", in.FilePath)
	}
	clean := filepath.Clean(in.FilePath)

	sessionID := ""
	if req != nil && req.Session != nil {
		sessionID = req.Session.ID()
	}

	// Pre-flight: symlink safety, read-before-overwrite, modified-since-read.
	if isSymlink(clean) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, clean) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if err := h.checkReadBeforeOverwrite(sessionID, clean, errModifiedPreFlight); err != nil {
		return nil, nil, err
	}

	// Per-path mutex covers the in-call TOCTOU re-check and the write.
	unlock := h.registry.LockPath(clean)
	defer unlock()

	// In-call TOCTOU re-check.
	if isSymlink(clean) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, clean) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if err := h.checkReadBeforeOverwrite(sessionID, clean, errModifiedInCallTOCTOU); err != nil {
		return nil, nil, err
	}

	// Ensure parent directory exists.
	parent := filepath.Dir(clean)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create parent dir %s: %w", parent, err)
	}

	contentBytes := []byte(in.Content)

	if err := writeAtomic(clean, contentBytes, h.logger); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", clean, err)
	}

	// Post-write size verification (network-mount truncation guard).
	info, err := os.Stat(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("post-write stat %s: %w", clean, err)
	}
	if info.Size() != int64(len(contentBytes)) {
		return nil, nil, fmt.Errorf(
			"post-write size mismatch for %s: got %d, want %d (the filesystem may have truncated the write)",
			clean, info.Size(), len(contentBytes),
		)
	}

	h.refreshState(sessionID, clean, contentBytes, info)

	return textResult(fmt.Sprintf("File %s has been written successfully.", clean)), nil, nil
}

// checkReadBeforeOverwrite enforces the spec's read-before-overwrite and
// modified-since-read rules for an existing target. A non-existent
// target returns nil (a new file does not require a prior Read).
// modifiedWording is the error string to emit for the
// modified-since-read failure; pre-flight and in-call paths pass
// different wordings as the spec requires.
func (h *writeHandler) checkReadBeforeOverwrite(sessionID, path, modifiedWording string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", path)
	}

	entry, ok := h.registry.Get(sessionID, path)
	if !ok || entry.IsPartialView {
		return errors.New(errExistingButUnread) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	currentMTime := info.ModTime().UnixMilli()
	if currentMTime <= entry.ModTimeMillis {
		return nil
	}

	// mtime advanced
	if entry.Offset != 0 || entry.Limit != 0 {
		// partial read: refuse unconditionally.
		return errors.New(modifiedWording)
	}

	// full read: try content-equality fallback.
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s for content fallback: %w", path, err)
	}
	normalised := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if hashContent(normalised) != entry.ContentHash {
		return errors.New(modifiedWording)
	}
	return nil
}

func (h *writeHandler) refreshState(sessionID, path string, content []byte, info os.FileInfo) {
	if sessionID == "" {
		return
	}
	normalised := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	h.registry.Seed(sessionID, path, ReadEntry{
		Content:       normalised,
		ContentHash:   hashContent(normalised),
		ModTimeMillis: info.ModTime().UnixMilli(),
		Offset:        0,
		Limit:         0,
	})
}

// isSymlink reports whether the path is a symbolic link. A non-existent
// path returns false (a new file is being created).
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// writeAtomic writes content to path atomically by creating a temporary
// file in the same directory, syncing, and renaming. If any step of the
// atomic path fails, it falls back to a direct (non-atomic) write and
// logs the fallback.
func writeAtomic(path string, content []byte, logger *slog.Logger) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ccfs-write-tmp-*")
	if err != nil {
		logger.Warn("write: atomic temp file creation failed, falling back to non-atomic write",
			"path", path, "err", err)
		return os.WriteFile(path, content, 0o644) //nolint:gosec // 0o644 matches the upstream Write tool's mode; world-readable by spec.
	}
	tmpName := tmp.Name()

	cleanup := func(reason string, cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		logger.Warn("write: atomic path failed, falling back to non-atomic write",
			"path", path, "stage", reason, "err", cause)
		return os.WriteFile(path, content, 0o644) //nolint:gosec // 0o644 matches the upstream Write tool's mode; world-readable by spec.
	}

	if _, err := tmp.Write(content); err != nil {
		return cleanup("write temp", err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup("sync temp", err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup("close temp", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		logger.Warn("write: chmod on temp file failed, falling back to non-atomic write",
			"path", path, "err", err)
		return os.WriteFile(path, content, 0o644) //nolint:gosec // 0o644 matches the upstream Write tool's mode; world-readable by spec.
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		logger.Warn("write: atomic rename failed, falling back to non-atomic write",
			"path", path, "err", err)
		return os.WriteFile(path, content, 0o644) //nolint:gosec // 0o644 matches the upstream Write tool's mode; world-readable by spec.
	}
	return nil
}
