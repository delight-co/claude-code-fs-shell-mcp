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
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EditInput is the JSON input schema for the edit tool.
type EditInput struct {
	FilePath   string `json:"file_path" jsonschema:"absolute path to the file to edit; must exist and have been read in this session unless old_string is empty"`
	OldString  string `json:"old_string" jsonschema:"exact text to replace; whitespace and indentation must match byte-for-byte"`
	NewString  string `json:"new_string" jsonschema:"text that replaces old_string"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"when true, replaces every occurrence of old_string; default false"`
}

// EditConfig configures the edit tool's tunable behaviour.
type EditConfig struct {
	// MaxFileSize is the largest file (in bytes) the tool will edit.
	// Defaults to 1 GiB, matching the upstream tool.
	MaxFileSize int64
}

// DefaultEditConfig returns the default edit tool configuration.
func DefaultEditConfig() EditConfig {
	return EditConfig{MaxFileSize: 1 << 30}
}

const editDescription = `Performs exact string replacements in files.

Usage:
- You must use your ` + "`Read`" + ` tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file.
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: line number + tab. Everything after that is the actual file content to match. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if ` + "`old_string`" + ` is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use ` + "`replace_all`" + ` to change every instance of ` + "`old_string`" + `.
- Use ` + "`replace_all`" + ` for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.`

// Edit-tool error wordings reproduced verbatim from docs/spec/edit.md.
const (
	errEditNoOp                 = "No changes to make: old_string and new_string are exactly the same."
	errEditFileTooLargeFormat   = "File is too large to edit (%s). Maximum editable file size is %s."
	errEditFileDoesNotExistFmt  = "File does not exist. Note: your current working directory is %s."
	errEditCannotCreateExisting = "Cannot create new file - file already exists."
	errEditIPYNBReject          = "File is a Jupyter Notebook. Use the NotebookEdit to edit this file."
	errEditStringNotFoundFmt    = "String to replace not found in file.\nString: %s"
	errEditMultipleMatchesFmt   = "Found %d matches of the string to replace, but replace_all is false. To replace all occurrences, set replace_all to true. To replace only one occurrence, please provide more context to uniquely identify the instance.\nString: %s"
	editUnicodeNoteSuffix       = "\n(note: Edit also tried swapping \\uXXXX escapes and their characters; neither form matched, so the mismatch is likely elsewhere in old_string. Re-read the file and copy the exact surrounding text.)"
)

// RegisterEdit adds the edit tool to the given MCP server.
//
// The handler uses `any` as its output type so the SDK does not populate
// the response's structuredContent field, matching the project-wide
// response transport convention.
//
// The registry parameter is required: the read-before-edit and
// modified-since-read contracts depend on the per-session read-tracking
// state seeded by the Read tool.
func RegisterEdit(s *mcp.Server, cfg EditConfig, logger *slog.Logger, registry ReadStateAccess) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &editHandler{cfg: cfg, logger: logger, registry: registry}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit",
		Description: editDescription,
	}, h.handle)
}

type editHandler struct {
	cfg      EditConfig
	logger   *slog.Logger
	registry ReadStateAccess
}

func (h *editHandler) handle(_ context.Context, req *mcp.CallToolRequest, in EditInput) (*mcp.CallToolResult, any, error) {
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

	// Pre-flight: no-op
	if in.OldString == in.NewString {
		return nil, nil, errors.New(errEditNoOp) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	// Pre-flight: stat
	info, statErr := os.Stat(clean)
	fileExists := statErr == nil
	if !fileExists && !errors.Is(statErr, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("stat %s: %w", clean, statErr)
	}
	if fileExists && info.IsDir() {
		return nil, nil, fmt.Errorf("path is a directory, not a file: %s", clean)
	}

	// Pre-flight: file size cap
	if fileExists && info.Size() > h.cfg.MaxFileSize {
		return nil, nil, fmt.Errorf(errEditFileTooLargeFormat, //nolint:staticcheck // spec-pinned wording ends with period.
			formatBytesBinary(info.Size()), formatBytesBinary(h.cfg.MaxFileSize))
	}

	// Create-mode branches.
	if !fileExists {
		if in.OldString != "" {
			cwd, _ := os.Getwd()
			return nil, nil, fmt.Errorf(errEditFileDoesNotExistFmt, cwd) //nolint:staticcheck // spec-pinned wording ends with period.
		}
		return h.createNewFile(sessionID, clean, in.NewString)
	}
	if in.OldString == "" {
		raw, err := os.ReadFile(clean)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", clean, err)
		}
		if strings.TrimSpace(string(raw)) != "" {
			return nil, nil, errors.New(errEditCannotCreateExisting) //nolint:staticcheck // spec-pinned wording ends with period.
		}
		return h.createNewFile(sessionID, clean, in.NewString)
	}

	// Pre-flight: .ipynb rejection
	if strings.EqualFold(filepath.Ext(clean), ".ipynb") {
		return nil, nil, errors.New(errEditIPYNBReject) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	// Pre-flight: symlink safety
	if isSymlink(clean) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, clean) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	// Pre-flight: read-before-edit + modified-since-read (shared helper).
	if err := checkReadBeforeMutation(h.registry, sessionID, clean, errModifiedPreFlight); err != nil {
		return nil, nil, err
	}

	// Pre-flight: match + uniqueness
	raw, err := os.ReadFile(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", clean, err)
	}
	text := string(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))

	matched := findMatch(text, in.OldString)
	if matched.Actual == "" {
		msg := fmt.Sprintf(errEditStringNotFoundFmt, in.OldString)
		if shouldAppendUnicodeNote(in.OldString) {
			msg += editUnicodeNoteSuffix
		}
		return nil, nil, errors.New(msg)
	}
	matchCount := strings.Count(text, matched.Actual)
	if matchCount > 1 && !in.ReplaceAll {
		return nil, nil, fmt.Errorf(errEditMultipleMatchesFmt, matchCount, in.OldString) //nolint:staticcheck // spec-pinned wording starts with capital.
	}

	// Per-path mutex covers the in-call TOCTOU re-check and the write.
	unlock := h.registry.LockPath(clean)
	defer unlock()

	if isSymlink(clean) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, clean) //nolint:staticcheck // spec-pinned wording ends with period.
	}
	if err := checkReadBeforeMutation(h.registry, sessionID, clean, errModifiedInCallTOCTOU); err != nil {
		return nil, nil, err
	}

	// Re-read + re-match in case content changed since pre-flight.
	raw2, err := os.ReadFile(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("re-read %s: %w", clean, err)
	}
	text2 := string(bytes.ReplaceAll(raw2, []byte("\r\n"), []byte("\n")))
	matched2 := findMatch(text2, in.OldString)
	if matched2.Actual == "" {
		return nil, nil, errors.New("String not found in file. Failed to apply edit.") //nolint:staticcheck // spec-pinned wording.
	}

	// Preprocess new_string for any escape-fallback synchronisation.
	preparedNew := in.NewString
	if matched2.DidEscapeFallback {
		preparedNew, _ = convertEscapeToChar(in.NewString)
	}

	updated := applyReplacement(text2, matched2.Actual, preparedNew, in.NewString, in.ReplaceAll)
	updatedBytes := []byte(updated)

	if err := writeAtomic(clean, updatedBytes, h.logger); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", clean, err)
	}

	postInfo, err := os.Stat(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("post-write stat %s: %w", clean, err)
	}
	if postInfo.Size() != int64(len(updatedBytes)) {
		return nil, nil, fmt.Errorf(
			"post-write size mismatch for %s: got %d, want %d (the filesystem may have truncated the write)",
			clean, postInfo.Size(), len(updatedBytes),
		)
	}

	refreshReadEntry(h.registry, sessionID, clean, updatedBytes, postInfo)

	return textResult(formatEditSuccess(clean, in.ReplaceAll)), nil, nil
}

// createNewFile handles the create-mode path (file does not exist, OR
// exists but is empty, AND old_string is empty). It performs the same
// atomic write + state refresh as a regular edit, but without the
// match / uniqueness checks.
func (h *editHandler) createNewFile(sessionID, path, content string) (*mcp.CallToolResult, any, error) {
	if isSymlink(path) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, path) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	unlock := h.registry.LockPath(path)
	defer unlock()

	if isSymlink(path) {
		return nil, nil, fmt.Errorf(errSymlinkRefusedFormat, path) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create parent dir %s: %w", parent, err)
	}

	contentBytes := []byte(content)
	if err := writeAtomic(path, contentBytes, h.logger); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("post-write stat %s: %w", path, err)
	}
	if info.Size() != int64(len(contentBytes)) {
		return nil, nil, fmt.Errorf(
			"post-write size mismatch for %s: got %d, want %d",
			path, info.Size(), len(contentBytes),
		)
	}

	refreshReadEntry(h.registry, sessionID, path, contentBytes, info)
	return textResult(formatEditSuccess(path, false)), nil, nil
}

// --- string matching ---

// matchResult is the outcome of findMatch.
type matchResult struct {
	// Actual is the substring of the file content that matched. May
	// differ from old_string (currently only via the unicode-escape
	// fallback). Empty when no match.
	Actual string
	// DidEscapeFallback is true when the match was found via strategy 3
	// (\uXXXX literal in old_string converted to real characters in the
	// file). new_string must also be converted.
	DidEscapeFallback bool
}

// findMatch implements a subset of the upstream tool's match chain:
// strategy 1 (exact substring) and strategy 3 (unicode-escape literal
// → real character). Strategies 2 (smart-quote normalisation) and 4
// (real character → unicode-escape regex) are not yet implemented; see
// docs/spec/edit.md Known gaps.
func findMatch(fileContent, oldString string) matchResult {
	if oldString == "" {
		return matchResult{}
	}
	if strings.Contains(fileContent, oldString) {
		return matchResult{Actual: oldString}
	}
	if hasUnicodeEscape(oldString) {
		converted, didConvert := convertEscapeToChar(oldString)
		if didConvert && strings.Contains(fileContent, converted) {
			return matchResult{Actual: converted, DidEscapeFallback: true}
		}
	}
	return matchResult{}
}

var unicodeEscapeRe = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

func hasUnicodeEscape(s string) bool {
	return unicodeEscapeRe.MatchString(s)
}

func hasNonAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// convertEscapeToChar converts `\uXXXX` literals in s to real
// characters, preserving `\\u` (escaped backslash + u). Returns the
// converted string and whether any conversion took place.
func convertEscapeToChar(s string) (string, bool) {
	var b strings.Builder
	didConvert := false
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\\' && s[i+1] == '\\' {
			b.WriteByte(s[i])
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if i+5 < len(s) && s[i] == '\\' && s[i+1] == 'u' &&
			isHex(s[i+2]) && isHex(s[i+3]) && isHex(s[i+4]) && isHex(s[i+5]) {
			code, err := strconv.ParseInt(s[i+2:i+6], 16, 32)
			if err == nil {
				b.WriteRune(rune(code))
				didConvert = true
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), didConvert
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// shouldAppendUnicodeNote decides whether the not-found error should
// carry the upstream-tool's hint about \uXXXX swap attempts.
func shouldAppendUnicodeNote(oldString string) bool {
	return hasUnicodeEscape(oldString) || hasNonAscii(oldString)
}

// --- replacement ---

// applyReplacement performs the actual string substitution, including
// the deletion + trailing-newline absorption rule for empty new_string.
// rawNew is the caller-supplied new_string (used to detect the deletion
// case); preparedNew is the version with any escape-fallback
// preprocessing already applied.
func applyReplacement(content, actual, preparedNew, rawNew string, replaceAll bool) string {
	if rawNew == "" && !strings.HasSuffix(actual, "\n") && strings.Contains(content, actual+"\n") {
		if replaceAll {
			return strings.ReplaceAll(content, actual+"\n", "")
		}
		return strings.Replace(content, actual+"\n", "", 1)
	}
	if replaceAll {
		return strings.ReplaceAll(content, actual, preparedNew)
	}
	return strings.Replace(content, actual, preparedNew, 1)
}

// --- success message ---

func formatEditSuccess(filePath string, replaceAll bool) string {
	if replaceAll {
		return fmt.Sprintf("The file %s has been updated. All occurrences were successfully replaced.", filePath)
	}
	return fmt.Sprintf("The file %s has been updated successfully.", filePath)
}

// --- file size formatting (binary units, matching upstream) ---

func formatBytesBinary(size int64) string {
	const (
		kib = 1024
		mib = 1024 * 1024
		gib = 1024 * 1024 * 1024
	)
	switch {
	case size >= gib:
		return fmt.Sprintf("%.0fGB", float64(size)/float64(gib))
	case size >= mib:
		return fmt.Sprintf("%.0fMB", float64(size)/float64(mib))
	case size >= kib:
		return fmt.Sprintf("%.0fKB", float64(size)/float64(kib))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
