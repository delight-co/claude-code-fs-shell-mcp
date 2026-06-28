package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GlobInput is the JSON input schema for the glob tool. Field names
// mirror the upstream Claude Code CLI's two-parameter shape exactly.
type GlobInput struct {
	Pattern string `json:"pattern" jsonschema:"the glob pattern to match files against"`
	Path    string `json:"path,omitempty" jsonschema:"the directory to search in; defaults to current working directory"`
}

// GlobConfig configures the glob tool's tunable behaviour. Values match
// the upstream defaults pinned in docs/spec/glob.md.
type GlobConfig struct {
	TimeoutMs      int    // default 20000 (upstream: 20s, 60s on WSL — env override only in this impl)
	OutputCapChars int    // default 50000 (upstream effective wrap cap)
	MaxResults     int    // default 100 (upstream REPL uses 25000)
	RipgrepPath    string // if empty, "rg" is looked up on PATH
}

// DefaultGlobConfig returns the default glob tool configuration.
func DefaultGlobConfig() GlobConfig {
	return GlobConfig{
		TimeoutMs:      20_000,
		OutputCapChars: 50_000,
		MaxResults:     100,
	}
}

const globDescription = `- Fast file pattern matching tool that works with any codebase size
- Supports glob patterns like "**/*.js" or "src/**/*.ts"
- Returns matching file paths sorted by modification time
- Use this tool when you need to find files by name patterns
- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the Agent tool instead`

// Error and notice string literals. Wording reproduced verbatim from
// docs/spec/glob.md so callers can pattern-match on them.
const (
	errGlobPatternRequired       = "pattern is required"
	errGlobDirNotExistFormat     = "Directory does not exist: %s. Note: your current working directory is %s."
	errGlobPathNotDirectoryFmt   = "Path is not a directory: %s"
	errGlobRipgrepNotFound       = "ripgrep not found on PATH. Install it (brew install ripgrep / apt install ripgrep / winget install BurntSushi.ripgrep.MSVC) or use the native claude binary which embeds it."
	errGlobTimeoutFormat         = "Ripgrep search timed out after %d seconds. The search may have matched files but did not complete in time. Try searching a more specific path or pattern."
	noticeGlobNoFilesFound       = "No files found"
	noticeGlobOutputCapTruncated = "\n\n[truncated: output exceeded the per-call cap]"
	// The upstream's v2.1.195-reachable truncation notice variant.
	noticeGlobTruncatedFmt = "(Showing %d of %d matching files; %d more are not listed. Narrow the pattern or path to see the rest.)"
)

// RegisterGlob adds the glob tool to the given MCP server.
//
// The handler uses any as its output type so the SDK does not populate
// the response's structuredContent field; all payload is returned via
// Content blocks only, matching the project-wide response transport
// convention.
func RegisterGlob(s *mcp.Server, cfg GlobConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 20_000
	}
	if cfg.OutputCapChars <= 0 {
		cfg.OutputCapChars = 50_000
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 100
	}
	h := &globHandler{cfg: cfg, logger: logger}
	mcp.AddTool(s, &mcp.Tool{Name: "glob", Description: globDescription}, h.handle)
}

type globHandler struct {
	cfg    GlobConfig
	logger *slog.Logger
}

func (h *globHandler) handle(ctx context.Context, _ *mcp.CallToolRequest, in GlobInput) (*mcp.CallToolResult, any, error) {
	if in.Pattern == "" {
		return nil, nil, errors.New(errGlobPatternRequired)
	}

	// Path validation: must exist and be a directory; skip stat for UNC paths.
	if in.Path != "" && !strings.HasPrefix(in.Path, `\\`) && !strings.HasPrefix(in.Path, "//") {
		info, err := os.Stat(in.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				cwd, _ := os.Getwd()
				return nil, nil, fmt.Errorf(errGlobDirNotExistFormat, in.Path, cwd) //nolint:staticcheck // spec-pinned wording ends with period.
			}
			return nil, nil, fmt.Errorf("path stat %s: %w", in.Path, err)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf(errGlobPathNotDirectoryFmt, in.Path) //nolint:staticcheck // spec-pinned wording starts with capital.
		}
	}

	rgPath, err := h.detectRipgrep()
	if err != nil {
		return nil, nil, errors.New(errGlobRipgrepNotFound) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	searchRoot, pattern := h.resolveSearchRoot(in)
	args := h.buildArgs(pattern)

	// Effective timeout: cfg default + env override.
	timeoutMs := h.cfg.TimeoutMs
	if envSec := os.Getenv("CLAUDE_CODE_GLOB_TIMEOUT_SECONDS"); envSec != "" {
		if n, parseErr := strconv.Atoi(envSec); parseErr == nil && n > 0 {
			timeoutMs = n * 1000
		}
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	args = append(args, searchRoot)
	cmd := exec.CommandContext(ctxTimeout, rgPath, args...) //nolint:gosec // rgPath is from PATH lookup; args are constructed from validated input.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if errors.Is(ctxTimeout.Err(), context.DeadlineExceeded) {
		return nil, nil, fmt.Errorf(errGlobTimeoutFormat, timeoutMs/1000) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			h.logger.Warn("glob exec error", "err", runErr)
		}
		// Exit codes 0 (matches), 1 (no matches), and 2 (invalid args /
		// bad glob) all fall through; exit 2 is silently suppressed per
		// spec, matching the upstream behaviour.
	}

	rawLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	files := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// rg returns paths relative to the search root; make absolute first.
		if !filepath.IsAbs(line) {
			line = filepath.Join(searchRoot, line)
		}
		files = append(files, line)
	}

	maxResults := h.cfg.MaxResults
	totalMatches := len(files)
	truncated := totalMatches > maxResults
	if truncated {
		files = files[:maxResults]
	}

	relPaths := make([]string, len(files))
	for i, p := range files {
		relPaths[i] = relativeToCwd(p)
	}

	formatted := h.formatResult(relPaths, truncated, len(files), totalMatches)

	if len(formatted) > h.cfg.OutputCapChars {
		formatted = formatted[:h.cfg.OutputCapChars] + noticeGlobOutputCapTruncated
	}

	return textResult(formatted), nil, nil
}

func (h *globHandler) detectRipgrep() (string, error) {
	if h.cfg.RipgrepPath != "" {
		if _, err := os.Stat(h.cfg.RipgrepPath); err == nil {
			return h.cfg.RipgrepPath, nil
		}
	}
	if path, err := exec.LookPath("rg"); err == nil {
		return path, nil
	}
	return "", errors.New("rg not found on PATH")
}

// resolveSearchRoot determines the rg search root and the (possibly
// rewritten) pattern. If the supplied pattern is absolute, the upstream
// Def splitter extracts a baseDir + relativePattern and silently
// overrides the caller's path with baseDir. We reproduce that behaviour
// for parity (see docs/spec/glob.md, Absolute-pattern handling).
func (h *globHandler) resolveSearchRoot(in GlobInput) (root, pattern string) {
	root = in.Path
	if root == "" {
		if pwd, err := os.Getwd(); err == nil {
			root = pwd
		}
	}
	pattern = in.Pattern
	if filepath.IsAbs(in.Pattern) {
		baseDir, relPat := splitAbsolutePattern(in.Pattern)
		if baseDir != "" {
			root = baseDir
			pattern = relPat
		}
	}
	return root, pattern
}

// splitAbsolutePattern extracts the static-prefix baseDir and the
// relativePattern from an absolute glob pattern, mirroring the upstream
// Def(pattern) helper.
//
//   - "/foo/bar/file.go" -> ("/foo/bar", "file.go")
//   - "/foo/bar/**/*.go" -> ("/foo/bar", "**/*.go")
//   - "/*.go"            -> ("/", "*.go")
//   - "**/*.go"          -> ("", "**/*.go") -- caller leaves path unchanged
func splitAbsolutePattern(pattern string) (baseDir, relativePattern string) {
	metaIdx := strings.IndexAny(pattern, "*?[{")
	if metaIdx < 0 {
		return filepath.Dir(pattern), filepath.Base(pattern)
	}
	staticPrefix := pattern[:metaIdx]
	slashIdx := strings.LastIndexByte(staticPrefix, '/')
	if slashIdx < 0 {
		return "", pattern
	}
	base := staticPrefix[:slashIdx]
	rel := pattern[slashIdx+1:]
	if base == "" && slashIdx == 0 {
		base = "/"
	}
	return base, rel
}

// buildArgs assembles the rg argument list (without the search root,
// which is appended at the call site).
func (h *globHandler) buildArgs(pattern string) []string {
	args := []string{"--files", "--glob", pattern, "--sort=modified"}

	if envTruthy(os.Getenv("CLAUDE_CODE_GLOB_NO_IGNORE"), true) {
		args = append(args, "--no-ignore")
	}
	if envTruthy(os.Getenv("CLAUDE_CODE_GLOB_HIDDEN"), true) {
		args = append(args, "--hidden")
	}
	return args
}

// envTruthy returns whether s is a truthy env string. Empty string falls
// back to defaultVal. Matches the upstream ut() helper:
// case-insensitive 1 / true / yes / on.
func envTruthy(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// formatResult renders the glob result content per docs/spec/glob.md.
func (h *globHandler) formatResult(paths []string, truncated bool, numShown, totalMatches int) string {
	if numShown == 0 {
		return noticeGlobNoFilesFound
	}
	body := strings.Join(paths, "\n")
	if truncated {
		notice := fmt.Sprintf(noticeGlobTruncatedFmt, numShown, totalMatches, totalMatches-numShown)
		body = body + "\n" + notice
	}
	return body
}
