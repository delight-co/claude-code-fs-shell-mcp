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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BashInput is the JSON input schema for the bash tool.
type BashInput struct {
	Command                   string `json:"command" jsonschema:"the shell command to execute"`
	Description               string `json:"description,omitempty" jsonschema:"short past-tense summary of what the command does (a few words); surfaced in UI rows"`
	Timeout                   int    `json:"timeout,omitempty" jsonschema:"per-command timeout in milliseconds; capped by the implementation"`
	RunInBackground           bool   `json:"run_in_background,omitempty" jsonschema:"when true, the command runs as a background task instead of blocking the tool call"`
	DangerouslyDisableSandbox bool   `json:"dangerouslyDisableSandbox,omitempty" jsonschema:"bypass the sandbox for this single call; the hosting environment may still refuse the bypass by policy"`
}

// BashConfig configures the bash tool's tunable behaviour. Values match
// the upstream Claude Code CLI defaults pinned in docs/spec/bash.md.
type BashConfig struct {
	DefaultTimeoutMs int      // default per-command timeout in milliseconds (spec default 120000)
	MaxTimeoutMs     int      // maximum per-command timeout in milliseconds (spec default 600000)
	OutputCapChars   int      // tool-result wrap cap (spec default 30000)
	ShellPath        string   // if empty, shell is detected from the env
	OriginalCwd      string   // project root; if empty, the process cwd at server startup is used
	AdditionalDirs   []string // additional directories considered inside the project boundary
}

// DefaultBashConfig returns the default bash tool configuration.
func DefaultBashConfig() BashConfig {
	return BashConfig{
		DefaultTimeoutMs: 120_000,
		MaxTimeoutMs:     600_000,
		OutputCapChars:   30_000,
	}
}

const bashDescription = `Executes a given bash command and returns its output.

The working directory persists between commands within a session, but shell state does not. The shell environment is initialized from the calling process's environment.`

// Error and notice string literals. Wording reproduced verbatim from
// docs/spec/bash.md so callers can pattern-match on them.
const (
	errBashCommandEmpty      = "command is required"
	errBashShellNotFound     = "No suitable shell found. Claude CLI requires a Posix shell environment. Please ensure you have a valid shell installed and the SHELL environment variable set."
	errBashTimeoutFormat     = "Command timed out after %s"
	errBashExitCodeFormat    = "Exit code %d"
	noticeCwdResetFormat     = "\nShell cwd was reset to %s"
	noticeRunInBgFallback    = "Note: this server does not yet support background tasks; the command was run in the foreground instead."
	noticeOutputCapTruncated = "\n\n[truncated: output exceeded the per-call cap]"
)

// RegisterBash adds the bash tool to the given MCP server.
//
// The handler uses any as its output type so the SDK does not populate
// the response's structuredContent field; all payload is returned via
// Content blocks only, matching the project-wide response transport
// convention.
//
// The registry parameter is accepted for forward compatibility with the
// tier 2 (read-equivalents seeding) follow-up but is unused in this
// initial tier 1 implementation.
func RegisterBash(s *mcp.Server, cfg BashConfig, logger *slog.Logger, registry ReadStateAccess) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.DefaultTimeoutMs <= 0 {
		cfg.DefaultTimeoutMs = 120_000
	}
	if cfg.MaxTimeoutMs <= 0 {
		cfg.MaxTimeoutMs = 600_000
	}
	if cfg.MaxTimeoutMs < cfg.DefaultTimeoutMs {
		cfg.MaxTimeoutMs = cfg.DefaultTimeoutMs
	}
	if cfg.OutputCapChars <= 0 {
		cfg.OutputCapChars = 30_000
	}
	if cfg.OriginalCwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			cfg.OriginalCwd = pwd
		}
	}
	h := &bashHandler{
		cfg:      cfg,
		logger:   logger,
		registry: registry,
		cwdState: newBashCwdState(),
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "bash",
		Description: bashDescription,
	}, h.handle)
}

type bashHandler struct {
	cfg      BashConfig
	logger   *slog.Logger
	registry ReadStateAccess
	cwdState *bashCwdState
}

func (h *bashHandler) handle(ctx context.Context, req *mcp.CallToolRequest, in BashInput) (*mcp.CallToolResult, any, error) {
	if in.Command == "" {
		return nil, nil, errors.New(errBashCommandEmpty)
	}

	sessionID := ""
	if req != nil && req.Session != nil {
		sessionID = req.Session.ID()
	}

	timeoutMs := in.Timeout
	if timeoutMs <= 0 {
		timeoutMs = h.cfg.DefaultTimeoutMs
	}
	if timeoutMs > h.cfg.MaxTimeoutMs {
		timeoutMs = h.cfg.MaxTimeoutMs
	}

	cwd := h.cwdState.get(sessionID)
	if cwd == "" {
		cwd = h.cfg.OriginalCwd
	}
	if cwd == "" {
		if pwd, err := os.Getwd(); err == nil {
			cwd = pwd
		}
	}
	cwd = recoverCwd(cwd)

	shellPath, err := h.detectShell()
	if err != nil {
		return nil, nil, errors.New(errBashShellNotFound) //nolint:staticcheck // spec-pinned wording ends with period.
	}

	env := h.buildEnv(shellPath)

	forcedForeground := in.RunInBackground

	cwdFile, err := os.CreateTemp("", "ccfs-bash-cwd-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create cwd sentinel file: %w", err)
	}
	cwdFilePath := cwdFile.Name()
	_ = cwdFile.Close()
	defer os.Remove(cwdFilePath)

	wrappedCmd := fmt.Sprintf("%s\npwd -P >| %s", in.Command, shellQuote(cwdFilePath))

	ctxTimeout, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, shellPath, "-c", wrappedCmd)
	cmd.Env = env
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	exitCode := 0
	timedOut := false
	if runErr != nil {
		if errors.Is(ctxTimeout.Err(), context.DeadlineExceeded) {
			timedOut = true
			if cmd.Process != nil {
				killProcessGroup(cmd.Process.Pid, h.logger)
			}
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if !timedOut {
			h.logger.Warn("bash exec error", "err", runErr)
			exitCode = -1
		}
	}

	newCwdBytes, _ := os.ReadFile(cwdFilePath)
	newCwd := strings.TrimSpace(string(newCwdBytes))
	cwdResetNotice := ""
	if newCwd != "" {
		switch {
		case newCwd == cwd:
			h.cwdState.set(sessionID, newCwd)
		case h.isInsideProject(newCwd):
			h.cwdState.set(sessionID, newCwd)
		default:
			h.cwdState.set(sessionID, h.cfg.OriginalCwd)
			cwdResetNotice = fmt.Sprintf(noticeCwdResetFormat, h.cfg.OriginalCwd)
		}
	}

	stdoutStr := strings.TrimRight(stdoutBuf.String(), "\n")
	stderrStr := strings.TrimRight(stderrBuf.String(), "\n")
	if timedOut {
		durationStr := formatDuration(time.Duration(timeoutMs) * time.Millisecond)
		stderrStr = appendLine(stderrStr, fmt.Sprintf(errBashTimeoutFormat, durationStr))
	}
	if exitCode != 0 && !timedOut {
		stderrStr = appendLine(stderrStr, fmt.Sprintf(errBashExitCodeFormat, exitCode))
	}
	if cwdResetNotice != "" {
		stderrStr = appendLine(stderrStr, cwdResetNotice)
	}

	parts := make([]string, 0, 4)
	if stdoutStr != "" {
		parts = append(parts, stdoutStr)
	}
	if stderrStr != "" {
		parts = append(parts, stderrStr)
	}
	if forcedForeground {
		parts = append(parts, noticeRunInBgFallback)
	}

	content := strings.Join(parts, "\n")

	if len(content) > h.cfg.OutputCapChars {
		content = content[:h.cfg.OutputCapChars] + noticeOutputCapTruncated
	}

	return textResult(content), nil, nil
}

// detectShell picks the shell binary path. Preference order: ShellPath
// override → CLAUDE_CODE_SHELL → SHELL → standard system locations.
func (h *bashHandler) detectShell() (string, error) {
	if h.cfg.ShellPath != "" {
		if _, err := os.Stat(h.cfg.ShellPath); err == nil {
			return h.cfg.ShellPath, nil
		}
	}
	candidates := []string{}
	if v := os.Getenv("CLAUDE_CODE_SHELL"); v != "" && (strings.Contains(v, "bash") || strings.Contains(v, "zsh")) {
		candidates = append(candidates, v)
	}
	if v := os.Getenv("SHELL"); v != "" && (strings.Contains(v, "bash") || strings.Contains(v, "zsh")) {
		candidates = append(candidates, v)
	}
	for _, base := range []string{"/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin"} {
		for _, name := range []string{"bash", "zsh"} {
			candidates = append(candidates, filepath.Join(base, name))
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() { //nolint:gosec // shell binary lookup; candidates are constrained to bash/zsh in standard locations or operator-supplied env vars.
			return c, nil
		}
	}
	return "", errors.New("no shell found")
}

// buildEnv constructs the child process env: the server's env minus the
// auth and OpenTelemetry vars, plus the upstream-mandated overrides.
func (h *bashHandler) buildEnv(shellPath string) []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent)+2)
	stripExact := map[string]struct{}{
		"CLAUDE_CODE_OAUTH_TOKEN":       {},
		"CLAUDE_CODE_SUBSCRIPTION_TYPE": {},
		"CLAUDE_CODE_RATE_LIMIT_TIER":   {},
		"CLAUDE_BG_AUTH_SNAPSHOT_PATH":  {},
		"CLAUDE_BG_SOCKET_TOKENS_PATH":  {},
		"CLAUDE_BG_RV_AUTH":             {},
		"CLAUDE_BG_PTY_AUTH":            {},
		"CLAUDE_CODE_OTEL_DIAG_STDERR":  {},
	}
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if _, drop := stripExact[key]; drop {
			continue
		}
		if strings.HasPrefix(key, "OTEL_") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_EDITOR=true")
	if shellPath != "" {
		out = append(out, "SHELL="+shellPath)
	}
	return out
}

// isInsideProject reports whether path is within the project boundary
// (OriginalCwd ∪ AdditionalDirs).
func (h *bashHandler) isInsideProject(path string) bool {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolvedPath = path
	}
	resolvedPath = filepath.Clean(resolvedPath)
	candidates := append([]string{h.cfg.OriginalCwd}, h.cfg.AdditionalDirs...)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		resolvedC, err := filepath.EvalSymlinks(c)
		if err != nil {
			resolvedC = c
		}
		resolvedC = filepath.Clean(resolvedC)
		if isSubpath(resolvedPath, resolvedC) {
			return true
		}
	}
	return false
}

// isSubpath reports whether child is parent itself or a descendant of it.
func isSubpath(child, parent string) bool {
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

// recoverCwd walks up the path until an existing directory is found.
func recoverCwd(cwd string) string {
	for cwd != "" && cwd != "/" {
		if info, err := os.Stat(cwd); err == nil && info.IsDir() {
			return cwd
		}
		cwd = filepath.Dir(cwd)
	}
	return "/"
}

// killProcessGroup sends SIGTERM to the process group of pid, waits up
// to 1500 ms, then sends SIGKILL if the process is still alive.
func killProcessGroup(pid int, logger *slog.Logger) {
	if pid <= 1 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		logger.Debug("bash kill: SIGTERM to process group failed", "pid", pid, "err", err)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		logger.Debug("bash kill: SIGKILL to process group failed", "pid", pid, "err", err)
	}
}

// formatDuration renders a duration like "1h2m" / "2m" / "30s" / "100ms".
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d >= time.Second:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
}

// shellQuote single-quotes s for safe inclusion in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appendLine joins existing and addition with a newline, omitting the
// separator when existing is empty.
func appendLine(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "\n" + addition
}

// bashCwdState tracks the persisted cwd per MCP session.
type bashCwdState struct {
	mu sync.Mutex
	m  map[string]string
}

func newBashCwdState() *bashCwdState {
	return &bashCwdState{m: map[string]string{}}
}

func (s *bashCwdState) get(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[sessionID]
}

func (s *bashCwdState) set(sessionID, cwd string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sessionID] = cwd
}
