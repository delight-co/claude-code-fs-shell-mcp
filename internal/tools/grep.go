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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GrepInput is the JSON input schema for the grep tool. Field names
// match the upstream Claude Code CLI's parameter names, including the
// handful that the upstream borrows verbatim from ripgrep flag names
// (`-A`, `-B`, `-C`, `-n`, `-i`, `-o`).
type GrepInput struct {
	Pattern    string `json:"pattern" jsonschema:"the regular expression pattern to search for in file contents"`
	Path       string `json:"path,omitempty" jsonschema:"file or directory to search in; defaults to current working directory"`
	Glob       string `json:"glob,omitempty" jsonschema:"glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\")"`
	Type       string `json:"type,omitempty" jsonschema:"file type to search (e.g. js, py, rust, go, java)"`
	OutputMode string `json:"output_mode,omitempty" jsonschema:"content / files_with_matches (default) / count"`
	A          *int   `json:"-A,omitempty" jsonschema:"lines after each match (content mode)"`
	B          *int   `json:"-B,omitempty" jsonschema:"lines before each match (content mode)"`
	C          *int   `json:"-C,omitempty" jsonschema:"alias for context"`
	Context    *int   `json:"context,omitempty" jsonschema:"lines before and after each match (content mode)"`
	N          *bool  `json:"-n,omitempty" jsonschema:"show line numbers (content mode); default true"`
	I          bool   `json:"-i,omitempty" jsonschema:"case-insensitive search"`
	O          bool   `json:"-o,omitempty" jsonschema:"print only the matched parts (content mode)"`
	Multiline  bool   `json:"multiline,omitempty" jsonschema:"enable multiline mode"`
	HeadLimit  *int   `json:"head_limit,omitempty" jsonschema:"limit output to first N entries; default 250; 0 = unlimited"`
	Offset     int    `json:"offset,omitempty" jsonschema:"skip first N entries before applying head_limit; default 0"`
}

// GrepConfig configures the grep tool's tunable behaviour. Values
// match the upstream defaults pinned in docs/spec/grep.md.
type GrepConfig struct {
	TimeoutMs      int    // default 20000 (the upstream is 20000 ms / 60000 ms on WSL)
	OutputCapChars int    // default 20000 (the upstream maxResultSizeChars)
	RipgrepPath    string // if empty, "rg" is looked up on PATH
}

// DefaultGrepConfig returns the default grep tool configuration.
func DefaultGrepConfig() GrepConfig {
	return GrepConfig{TimeoutMs: 20_000, OutputCapChars: 20_000}
}

const grepDescription = `Search file contents for patterns using ripgrep.

Pass a regex pattern and optionally narrow the search with glob / type / path.

Output modes:
- "files_with_matches" (default): file paths only
- "content": matching lines with surrounding context
- "count": match counts per file`

// Error and notice string literals. Wording reproduced verbatim from
// docs/spec/grep.md so callers can pattern-match on them.
const (
	errGrepPatternRequired       = "pattern is required"
	errGrepNullByteFormat        = "Grep %s cannot contain null bytes (\\0). Remove the null byte and try again."
	errGrepPathNotExistFormat    = "Path does not exist: %s. Note: your current working directory is %s."
	errGrepRipgrepNotFound       = "ripgrep not found on PATH. Install it (brew install ripgrep / apt install ripgrep / winget install BurntSushi.ripgrep.MSVC) or use the native claude binary which embeds it."
	errGrepTimeoutFormat         = "Ripgrep search timed out after %d seconds. The search may have matched files but did not complete in time. Try searching a more specific path or pattern."
	noticeGrepOutputCapTruncated = "\n\n[truncated: output exceeded the per-call cap]"
	noticeGrepNoMatchesFound     = "No matches found"
	noticeGrepNoFilesFound       = "No files found"
)

// RegisterGrep adds the grep tool to the given MCP server.
//
// The handler uses any as its output type so the SDK does not populate
// the response's structuredContent field; all payload is returned via
// Content blocks only, matching the project-wide response transport
// convention.
func RegisterGrep(s *mcp.Server, cfg GrepConfig, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 20_000
	}
	if cfg.OutputCapChars <= 0 {
		cfg.OutputCapChars = 20_000
	}
	h := &grepHandler{cfg: cfg, logger: logger}
	mcp.AddTool(s, &mcp.Tool{Name: "grep", Description: grepDescription}, h.handle)
}

type grepHandler struct {
	cfg    GrepConfig
	logger *slog.Logger
}

func (h *grepHandler) handle(ctx context.Context, _ *mcp.CallToolRequest, in GrepInput) (*mcp.CallToolResult, any, error) {
	if in.Pattern == "" {
		return nil, nil, errors.New(errGrepPatternRequired)
	}

	for _, pair := range []struct{ name, value string }{
		{"pattern", in.Pattern},
		{"path", in.Path},
		{"glob", in.Glob},
		{"type", in.Type},
	} {
		if strings.ContainsRune(pair.value, '\x00') {
			return nil, nil, fmt.Errorf(errGrepNullByteFormat, pair.name) //nolint:staticcheck // spec-pinned wording ends with period.
		}
	}

	if in.Path != "" && !strings.HasPrefix(in.Path, `\\`) && !strings.HasPrefix(in.Path, "//") {
		if _, err := os.Stat(in.Path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				cwd, _ := os.Getwd()
				return nil, nil, fmt.Errorf(errGrepPathNotExistFormat, in.Path, cwd) //nolint:staticcheck // spec-pinned wording ends with period.
			}
			return nil, nil, fmt.Errorf("path stat %s: %w", in.Path, err)
		}
	}

	rgPath, err := h.detectRipgrep()
	if err != nil {
		return nil, nil, errors.New(errGrepRipgrepNotFound) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	args := h.buildArgs(in)

	ctxTimeout, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.TimeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, rgPath, args...) //nolint:gosec // rgPath is from PATH lookup; args are constructed from validated input.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if errors.Is(ctxTimeout.Err(), context.DeadlineExceeded) {
		return nil, nil, fmt.Errorf(errGrepTimeoutFormat, h.cfg.TimeoutMs/1000) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			h.logger.Warn("grep exec error", "err", runErr)
		}
		// Exit codes 0 (matches), 1 (no matches), 2 (invalid regex / bad args)
		// all fall through; exit 2 is silently suppressed per spec, which
		// matches the upstream behaviour and is deliberate.
	}

	rawLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}

	outputMode := in.OutputMode
	if outputMode == "" {
		outputMode = "files_with_matches"
	}

	if outputMode == "files_with_matches" && len(lines) > 0 {
		sortFilesByMTimeDesc(lines)
	}

	headLimit := 250
	if in.HeadLimit != nil {
		headLimit = *in.HeadLimit
	}
	if headLimit < 0 {
		headLimit = 0
	}

	formatted := h.formatResult(outputMode, lines, headLimit, in.Offset)

	if len(formatted) > h.cfg.OutputCapChars {
		formatted = formatted[:h.cfg.OutputCapChars] + noticeGrepOutputCapTruncated
	}

	return textResult(formatted), nil, nil
}

// detectRipgrep picks the ripgrep binary path. Preference order:
// configured RipgrepPath override → PATH lookup for "rg".
func (h *grepHandler) detectRipgrep() (string, error) {
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

// buildArgs assembles the rg argument list per docs/spec/grep.md.
func (h *grepHandler) buildArgs(in GrepInput) []string {
	args := []string{"--hidden"}
	for _, dir := range []string{".git", ".svn", ".hg", ".bzr", ".jj", ".sl"} {
		args = append(args, "--glob", "!"+dir)
	}
	args = append(args, "--max-columns", "500")

	if in.Multiline {
		args = append(args, "-U", "--multiline-dotall")
	}
	if in.I {
		args = append(args, "-i")
	}

	outputMode := in.OutputMode
	if outputMode == "" {
		outputMode = "files_with_matches"
	}
	switch outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c", "-H")
	case "content":
		showLineNumbers := true
		if in.N != nil {
			showLineNumbers = *in.N
		}
		if showLineNumbers {
			args = append(args, "-n")
		}
		if in.O {
			args = append(args, "-o")
		}
		// Context flag precedence: context > -C > (-B, -A).
		switch {
		case in.Context != nil:
			args = append(args, "-C", strconv.Itoa(*in.Context))
		case in.C != nil:
			args = append(args, "-C", strconv.Itoa(*in.C))
		default:
			if in.B != nil {
				args = append(args, "-B", strconv.Itoa(*in.B))
			}
			if in.A != nil {
				args = append(args, "-A", strconv.Itoa(*in.A))
			}
		}
	}

	if strings.HasPrefix(in.Pattern, "-") {
		args = append(args, "-e", in.Pattern)
	} else {
		args = append(args, in.Pattern)
	}

	if in.Type != "" {
		args = append(args, "--type", in.Type)
	}

	if in.Glob != "" {
		for _, seg := range strings.Fields(in.Glob) {
			if strings.Contains(seg, "{") && strings.Contains(seg, "}") {
				args = append(args, "--glob", seg)
			} else {
				for _, sub := range strings.Split(seg, ",") {
					if sub != "" {
						args = append(args, "--glob", sub)
					}
				}
			}
		}
	}

	searchRoot := in.Path
	if searchRoot == "" {
		if pwd, err := os.Getwd(); err == nil {
			searchRoot = pwd
		}
	}
	args = append(args, searchRoot)
	return args
}

// formatResult renders the rg output lines into the wrap-cap-bound
// content string per docs/spec/grep.md output formatting rules.
func (h *grepHandler) formatResult(outputMode string, lines []string, headLimit, offset int) string {
	items, appliedLimit := paginate(lines, headLimit, offset)
	pagSuffix := buildPaginationSuffix(appliedLimit, offset)

	switch outputMode {
	case "files_with_matches":
		if len(items) == 0 {
			return noticeGrepNoFilesFound
		}
		relPaths := make([]string, len(items))
		for i, p := range items {
			relPaths[i] = relativeToCwd(p)
		}
		header := fmt.Sprintf("Found %d %s", len(relPaths), pluraliseFile(len(relPaths)))
		if pagSuffix != "" {
			header += " " + pagSuffix
		}
		return header + "\n" + strings.Join(relPaths, "\n")

	case "content":
		if len(items) == 0 {
			return noticeGrepNoMatchesFound
		}
		renamed := make([]string, len(items))
		for i, line := range items {
			renamed[i] = relativiseGrepLine(line)
		}
		body := strings.Join(renamed, "\n")
		if pagSuffix != "" {
			body += "\n\n[Showing results with pagination = " + pagSuffix + "]"
		}
		return body

	case "count":
		numFiles := 0
		numMatches := 0
		renamed := make([]string, 0, len(items))
		for _, line := range items {
			idx := strings.LastIndex(line, ":")
			if idx > 0 {
				count, err := strconv.Atoi(line[idx+1:])
				if err == nil && count > 0 {
					numFiles++
					numMatches += count
				}
			}
			renamed = append(renamed, relativiseGrepLine(line))
		}
		var body string
		if len(items) == 0 {
			body = noticeGrepNoMatchesFound
		} else {
			body = strings.Join(renamed, "\n")
		}
		body += fmt.Sprintf("\n\nFound %d total %s across %d %s.",
			numMatches, pluraliseOccurrence(numMatches),
			numFiles, pluraliseFile(numFiles))
		if pagSuffix != "" {
			body += " with pagination = " + pagSuffix
		}
		return body
	}
	return ""
}

// paginate slices items by (offset, limit). Returns the slice and the
// applied limit (nil if the cap did not trim anything).
func paginate(items []string, limit, offset int) ([]string, *int) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []string{}, nil
	}
	if limit == 0 {
		return items[offset:], nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	result := items[offset:end]
	if len(items)-offset > limit {
		l := limit
		return result, &l
	}
	return result, nil
}

// buildPaginationSuffix returns "limit: N[, offset: M]" or "" when no
// pagination applied.
func buildPaginationSuffix(appliedLimit *int, offset int) string {
	parts := make([]string, 0, 2)
	if appliedLimit != nil {
		parts = append(parts, fmt.Sprintf("limit: %d", *appliedLimit))
	}
	if offset != 0 {
		parts = append(parts, fmt.Sprintf("offset: %d", offset))
	}
	return strings.Join(parts, ", ")
}

// sortFilesByMTimeDesc sorts paths in place by mtime descending; ties
// are broken by path ascending. Paths whose stat fails are treated as
// mtime 0.
func sortFilesByMTimeDesc(paths []string) {
	type entry struct {
		path  string
		mtime int64
	}
	entries := make([]entry, len(paths))
	for i, p := range paths {
		if info, err := os.Stat(p); err == nil {
			entries[i] = entry{p, info.ModTime().UnixNano()}
		} else {
			entries[i] = entry{p, 0}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].mtime != entries[j].mtime {
			return entries[i].mtime > entries[j].mtime
		}
		return entries[i].path < entries[j].path
	})
	for i, e := range entries {
		paths[i] = e.path
	}
}

// relativeToCwd converts an absolute path to cwd-relative when inside
// cwd; otherwise returns the path unchanged.
func relativeToCwd(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// relativiseGrepLine converts the path part of a rg output line
// (`path:line:match`, `path:match`, or `path:count`) to cwd-relative.
// An initial Windows drive-letter colon (e.g. "C:") is skipped so it
// is not confused with the path/rest separator.
func relativiseGrepLine(line string) string {
	start := 0
	if len(line) >= 2 && isASCIILetter(line[0]) && line[1] == ':' {
		start = 2
	}
	idx := strings.Index(line[start:], ":")
	if idx < 0 {
		return line
	}
	idx += start
	pathPart := line[:idx]
	rest := line[idx:]
	return relativeToCwd(pathPart) + rest
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func pluraliseFile(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func pluraliseOccurrence(n int) string {
	if n == 1 {
		return "occurrence"
	}
	return "occurrences"
}
