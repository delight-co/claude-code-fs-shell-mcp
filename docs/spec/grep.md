# Grep

## Purpose

Search file contents for patterns. Where [`Glob`](./glob.md) finds files by name, `Grep` finds lines inside them. The tool is the agent's primary instrument for code search: a regex, an optional file scope, an output mode, and (for the content mode) the usual surrounding-lines controls.

The search engine is **ripgrep**: this tool is a structured wrapper around the `rg` binary, not a custom matcher. The wrapper layers in the search-root resolution, the project's VCS-directory exclusions, deny-rule conversions, and the response shaping the agent expects.

## Signature

```json
{
  "name": "grep",
  "input_schema": {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "The regular expression pattern to search for in file contents"
      },
      "path": {
        "type": "string",
        "description": "File or directory to search in (rg PATH). Defaults to current working directory."
      },
      "glob": {
        "type": "string",
        "description": "Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\") - maps to rg --glob"
      },
      "type": {
        "type": "string",
        "description": "File type to search (rg --type). Common types: js, py, rust, go, java, etc. More efficient than include for standard file types."
      },
      "output_mode": {
        "type": "string",
        "enum": ["content", "files_with_matches", "count"],
        "default": "files_with_matches",
        "description": "Output mode: \"content\" shows matching lines (supports -A/-B/-C context, -n line numbers, head_limit), \"files_with_matches\" shows file paths (supports head_limit), \"count\" shows match counts (supports head_limit). Defaults to \"files_with_matches\"."
      },
      "-A": {
        "type": "integer",
        "description": "Number of lines to show after each match (rg -A). Requires output_mode: \"content\", ignored otherwise."
      },
      "-B": {
        "type": "integer",
        "description": "Number of lines to show before each match (rg -B). Requires output_mode: \"content\", ignored otherwise."
      },
      "-C": {
        "type": "integer",
        "description": "Alias for context."
      },
      "context": {
        "type": "integer",
        "description": "Number of lines to show before and after each match (rg -C). Requires output_mode: \"content\", ignored otherwise."
      },
      "-n": {
        "type": "boolean",
        "default": true,
        "description": "Show line numbers in output (rg -n). Requires output_mode: \"content\", ignored otherwise. Defaults to true."
      },
      "-i": {
        "type": "boolean",
        "default": false,
        "description": "Case insensitive search (rg -i)"
      },
      "-o": {
        "type": "boolean",
        "default": false,
        "description": "Print only the matched (non-empty) parts of each matching line, one match per output line (rg -o / --only-matching). Requires output_mode: \"content\", ignored otherwise. Defaults to false."
      },
      "multiline": {
        "type": "boolean",
        "default": false,
        "description": "Enable multiline mode where . matches newlines and patterns can span lines (rg -U --multiline-dotall). Default: false."
      },
      "head_limit": {
        "type": "integer",
        "default": 250,
        "description": "Limit output to first N lines/entries, equivalent to \"| head -N\". Works across all output modes: content (limits output lines), files_with_matches (limits file paths), count (limits count entries). Defaults to 250 when unspecified. Pass 0 for unlimited (use sparingly — large result sets waste context)."
      },
      "offset": {
        "type": "integer",
        "default": 0,
        "description": "Skip first N lines/entries before applying head_limit, equivalent to \"| tail -n +N | head -N\". Works across all output modes. Defaults to 0."
      }
    },
    "required": ["pattern"]
  }
}
```

The schema mirrors the upstream `Grep` tool's parameters, including the upstream's slightly unusual choice of using `-A` / `-B` / `-C` / `-n` / `-i` / `-o` (verbatim ripgrep flag names) as JSON keys alongside the more conventional `output_mode` / `glob` / `type` / `multiline` / `head_limit` / `offset`. Per upstream, unknown keys are rejected.

## Capability boundaries

| Tier | What it covers | Status in this server |
| ---- | -------------- | --------------------- |
| **1 — Self-contained** | ripgrep binary resolution, args assembly, output formatting per mode, timeout, the byte-exact error / notice wording, the wrap cap with `<persisted-output>` envelope. | **In scope for the initial implementation.** |
| **2 — fs-tool integration** | (Grep does not seed the read-tracking state; not applicable.) | n/a |
| **3 — Sibling-tool integration** | (Grep does not depend on other tool families; not applicable.) | n/a |
| **4a — Architecturally infeasible** | PostToolUse hooks (memory-directory tracking after a Grep call), REPL-inner native timeout, the `Fpt()` removal-when-shell-available behaviour, the embedded-ripgrep re-exec path. | **Not reproducible** (Known limitations) |
| **4b — Implementable but deferred** | Orphaned-worktree exclusions, deny-rule path → `rg --glob !` conversion, auto-classifier input, model-conditional description prompt, first-use availability check. | **Deferred** (Known gaps) |

## Semantics

### Search engine

The engine is **ripgrep** (`rg`). The MCP server expects `rg` to be available on the hosting environment's `PATH`. The upstream CLI additionally supports two extra modes (an embedded `rg` inside its Bun-bundled binary, and a `USE_BUILTIN_RIPGREP` env-var preference); these are out of scope for an interposed MCP server, which deploys against whatever shell environment the hosting container provides.

A first-use availability test (`rg --version` returning stdout that starts with `ripgrep `) is recorded as a Known gap; the implementation may add it later if observation shows it useful for early-failure messaging.

### Default args (always passed)

For every search the wrapper passes:

- `--hidden` — include dotfiles (rg skips them by default).
- `--glob '!.git'`, `--glob '!.svn'`, `--glob '!.hg'`, `--glob '!.bzr'`, `--glob '!.jj'`, `--glob '!.sl'` — exclude VCS internal directories.
- `--max-columns 500` — truncate lines longer than 500 columns in the rg output.

### Mode-specific args

- `output_mode: "files_with_matches"` (default): `-l`.
- `output_mode: "content"`: no mode flag; `-n` (line numbers) is on by default; `-A` / `-B` / `-C` / `context` / `-o` are pushed when set.
- `output_mode: "count"`: `-c -H` (the `-H` forces file-name prefixing even for single-file output).

### Context flags

When `output_mode == "content"`:

- If `context` is set: `-C <context>`.
- Else if `-C` is set: `-C <-C>`.
- Else: push `-B <-B>` if set, push `-A <-A>` if set.

When `output_mode != "content"`, none of these flags are emitted (matching the schema text "Requires `output_mode: \"content\"`, ignored otherwise").

### Pattern handling

- The regex syntax is ripgrep's. Regex metacharacters that the caller wants literal need escaping (for example, finding `interface{}` takes the pattern `interface\{\}`).
- If `pattern` starts with `-`, the wrapper prepends `-e` to disambiguate it from a flag.
- `multiline: true` enables `-U --multiline-dotall`.
- `-i: true` enables `-i` (case-insensitive).

### Path handling

- `path` is the search root. If omitted, the search runs from the working directory at call time.
- `path` may be a directory **or** a file (`validateInput` does not reject a file path; rg handles both).
- `glob` accepts whitespace-separated globs; each segment is then split on `,` *unless* it contains both `{` and `}` (brace groups are preserved). Each surviving glob becomes `--glob <segment>`.
- `type` becomes `--type <type>`.

### Pagination (`head_limit` + `offset`)

- `head_limit` default is `250`; pass `0` for **unlimited**.
- `offset` default is `0`.
- Pagination applies after rg returns its lines; the items are sliced as `lines.slice(offset, offset + limit)`.
- An `appliedLimit` is reported in the structured response only when the cap actually trimmed something (i.e., `lines.length - offset > limit`).

### Timeout

- Default `20 000 ms`, increased to `60 000 ms` on WSL.
- Overridable via the `CLAUDE_CODE_GLOB_TIMEOUT_SECONDS` env var (positive integer; the value × 1000 replaces the default).
- A timeout sends `SIGTERM` to the rg child, then `SIGKILL` 5 seconds later.

### Output formatting per mode

The wrapped tool-result content is formatted text (the structured `data` object is a separate concern, surfaced to PostToolUse hooks but not to the model):

- **`files_with_matches` (default)**:
  - `numFiles == 0`: `No files found`.
  - Otherwise: `Found <N> file(s)<pagination?>\n<path>\n<path>\n...` where each path is made relative to the working directory when inside it, and the file list is sorted by mtime descending (ties broken by file path ascending). `pagination` is the optional pagination suffix described below.

- **`content`**:
  - Empty: `No matches found`.
  - Otherwise: the rg lines (each prefixed with `<path>:<line>:<match>` with `-n`, or `<path>:<match>` without) joined by `\n`. If pagination applied: trailing `\n\n[Showing results with pagination = <pagination>]`.

- **`count`**:
  - Empty: `No matches found` followed by trailing `\n\nFound 0 total occurrences across 0 files.`.
  - Otherwise: the rg `<path>:<count>` lines joined by `\n`, then trailing `\n\nFound <N> total occurrence(s) across <M> file(s).<pagination?>`.

Pagination suffix shape:

```
limit: <head_limit>[, offset: <offset>]
```

(`offset` is omitted when zero.)

### Wrap cap

The tool's `maxResultSizeChars` is **`20000` characters**. When the formatted content exceeds it, the same `<persisted-output>` envelope used by Bash replaces the content:

```
<persisted-output>
Output too large (<size>). Full output saved to: <path>

Preview (first 2.00KB):
<first ~2000 chars>
...
</persisted-output>
```

The trailing `...\n` is omitted when the preview equals the entire output. When the rendered content is empty, the wrap pipeline substitutes `(Grep completed with no output)`.

This MCP server's initial implementation uses a simplified single-tier cap: when the assembled tool-result content exceeds `OutputCapChars` (default `20000`), the content is truncated and `\n\n[truncated: output exceeded the per-call cap]` is appended. The full `<persisted-output>` envelope (with persisted-file path + preview block) is recorded as a Known gap; the same simplification applies to the Bash tool's tier 1 output handling.

## Errors and notices

Errors are surfaced as MCP tool errors, wrapped by the tool runner as `<tool_use_error>...</tool_use_error>`. Wording is pinned below.

### `validateInput`

- **Null byte in `pattern` / `path` / `glob` / `type`** (`errorCode: 2`):
  ```
  Grep <param-name> cannot contain null bytes (\0). Remove the null byte and try again.
  ```
  (`<param-name>` is the literal parameter name that triggered the failure.)

- **`path` does not exist** (`errorCode: 1`):
  ```
  Path does not exist: <path>. Note: your current working directory is <cwd>.
  ```
  When a likely candidate file exists nearby (computed via the upstream's similar-file finder), a suffix is appended:
  ```
   Did you mean <suggested>?
  ```

- **UNC paths (`\\…` / `//…`)** bypass the stat check and pass validation unchanged.

### Spawn / runtime errors

- **ripgrep binary not found on `PATH`** (`code === "ENOENT"`):
  ```
  ripgrep not found on PATH. Install it (brew install ripgrep / apt install ripgrep / winget install BurntSushi.ripgrep.MSVC) or use the native claude binary which embeds it.
  ```

- **Timeout** (signal `SIGTERM` / `SIGKILL`, or `ABORT_ERR` whose reason is `TimeoutError`):
  ```
  Ripgrep search timed out after <N> seconds. The search may have matched files but did not complete in time. Try searching a more specific path or pattern.
  ```
  (`<N>` is `60` on WSL, `20` elsewhere; or the env override.)

- **User-initiated cancellation** (abort whose reason name is not `TimeoutError`):
  ```
  [Request interrupted by user for tool use]
  ```

- **Invalid regex or other ripgrep usage errors (rg exit code 2)**: **silently suppressed**. The wrapper resolves with whatever partial stdout produced (typically empty) and the user receives a "no matches" / "no files" result with no error wording. This matches upstream behaviour and is deliberate.

- **Permission errors (`EACCES` / `EPERM`)**: propagated as the underlying spawn error message wrapped by `<tool_use_error>Error: ...</tool_use_error>`.

### Result-rendering wordings

| Condition | Wording |
| --- | --- |
| `content`, empty | `No matches found` |
| `content`, with pagination | `<content>\n\n[Showing results with pagination = <pagination>]` |
| `count`, empty | `No matches found\n\nFound 0 total occurrences across 0 files.` |
| `count`, with content | `<content>\n\nFound <N> total <occurrence\|occurrences> across <M> <file\|files>.<pagination?>` |
| `files_with_matches`, no files | `No files found` |
| `files_with_matches`, with files | `Found <N> <file\|files>[ <pagination>]\n<path>\n<path>\n...` |

The pagination suffix is `limit: <N>[, offset: <M>]` (offset omitted when zero).

## Permissions and security

- The upstream `Grep` tool is classed as **read-only** (`isReadOnly() === true`) and as a search command (`isSearchOrReadCommand()` returns `{ isSearch: true, isRead: false }`). It does **not** require a permission prompt in the upstream's default permission model.
- Permission rules use the `Read(<path-pattern>)` family — the same rules that gate `Read` / `Glob`. The upstream's flow walks the search root's ancestors and applies any matching Read-deny / Read-ask / Read-allow rule.
- `ruleContentField` is `"path"`: user-authored `Grep(<...>)` rules are interpreted as **path globs**, not regex patterns.
- The MCP server is **sandbox-agnostic**: it does not maintain a permission system of its own and does not wrap the rg child process in an OS-level sandbox. The hosting environment is expected to constrain which paths are reachable.

## Implementation status

🟢 **Drafted.** The spec is essentially complete from public sources and the v2.1.195 verification; the implementation will land in a follow-up PR. See [README](./README.md) for the project-wide matrix.

## Known gaps

These are gaps the implementation pull request will close, either by choosing a concrete behaviour or by reporting observed behaviour against the pinned CLI version.

- **(Tier 4b) Orphaned-worktree exclusions.** The upstream tool discovers `.orphaned_at` marker files under the CLI's cache directory and emits one `--glob '!**/<dir>/**'` per orphaned worktree. The MCP server has no equivalent cache directory model and skips this step. Searches rooted under such a cache would therefore see the orphaned dirs; in practice the MCP server's search roots are project trees, where this exclusion is a no-op upstream too.
- **(Tier 4b) Deny-rule path → `rg --glob !` conversion.** The upstream tool walks the agent's per-tool `Read`-deny rule map and converts each pattern into a glob exclusion for rg, so a denied path is not even listed in `files_with_matches`. The MCP server does not run a permission system of its own and skips this step; permission-style restrictions are deferred to the hosting environment.
- **(Tier 4b) Auto-classifier input** (`toAutoClassifierInput`). Upstream provides a short string (`<pattern> in <path>`) for policy decisions; this MCP server has no auto-classification surface.
- **(Tier 4b) Model-conditional description prompt.** The upstream tool ships a *compact* prompt for non-Anthropic / unknown models and a *full* one otherwise. An MCP server exposes a single prompt per tool registration; the implementation will pick the full one (a single registration cannot route per model).
- **(Tier 4b) First-use `rg --version` availability check.** The upstream tool fires a `tengu_ripgrep_availability` telemetry event before the first Grep call. The MCP server skips the check by default to avoid an extra spawn per process lifetime; the implementation may add it as an opt-in if observation shows it useful for early-failure messaging.
- **Behaviour when `head_limit < 0`.** The schema accepts any number; the upstream wrapper has no explicit clamp. The MCP server's implementation will clamp negative values to `0` (= unlimited) at impl time and record the choice here.
- **Behaviour when `path` is a file rather than a directory.** Upstream `validateInput` does not reject this; rg handles single-file paths transparently. The MCP server will match the upstream behaviour and add an integration test to confirm.
- **(Tier 4b) WSL auto-detection for the 60s timeout.** The initial impl uses the 20s default everywhere; deployments on WSL should set `CLAUDE_CODE_GLOB_TIMEOUT_SECONDS` (e.g., to 60) to match the upstream's WSL behaviour. The auto-detection lands in a follow-up. The same gap exists for Glob (and was added when Glob was implemented).
- **(Tier 4b) Wrap cap simplification.** The initial implementation uses a single-tier truncate notice (`[truncated: output exceeded the per-call cap]`) instead of the full `<persisted-output>` envelope; see [Wrap cap](#wrap-cap). The full envelope (with persisted-file path + preview block) lands in a follow-up alongside the Bash tool's equivalent simplification.

## Known limitations

These behaviours of the upstream Claude Code CLI's built-in `Grep` cannot be reproduced by an MCP server interposed between the CLI and the filesystem. They are recorded here so callers know which built-in behaviours are not available through this server.

- **(Tier 4a) PostToolUse hooks.** The upstream CLI fires hooks after every successful Grep call; the memory-directory tracking telemetry (`tengu_memdir_accessed` / `tengu_team_mem_accessed`) and any user-configured PostToolUse hooks run in that flow. The MCP server has no in-CLI hook surface.
- **(Tier 4a) REPL-inner native timeout.** The upstream CLI applies an additional `10 000 ms` REPL-inner wrapper timeout for tools in the `["Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit", "TodoWrite", "TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskUpdate"]` set. This is independent of the ripgrep child-process timeout and applies to the REPL call mechanism only. An MCP server has no REPL-inner layer to wrap.
- **(Tier 4a) `Fpt()` tool removal in local-agent + POSIX shell context.** The upstream CLI may *remove* `Grep` and `Glob` from the tool set under specific conditions (local-agent mode with a POSIX shell available) and substitute a hint message suggesting the agent use the shell `grep` instead. An MCP server cannot remove its own tools mid-session in this way; the tool is always present.
- **(Tier 4a) Embedded ripgrep mode.** The upstream Bun-bundled `claude` binary re-executes itself with `argv0=rg` to invoke an embedded ripgrep. An MCP server is a standalone process and cannot re-exec a parent host binary; the server relies on `rg` being on `PATH` in the hosting environment.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/):
  - [`tools-reference`](https://code.claude.com/docs/en/tools-reference) — Grep tool behavior section.
  - [`permissions`](https://code.claude.com/docs/en/permissions) — Read and Edit section (note: `Read(...)` rules apply to `Grep` and `Glob` on a best-effort basis).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0` (Claude Code CLI v2.1.191, 2026-06-24): `tool-description-grep.md`, `tool-description-grep-compact.md`.
- [`1rgs/nanocode`](https://github.com/1rgs/nanocode): the `grep` tool implementation, used as an independent cross-check for the default output mode and the empty-result semantics.

Verified against Claude Code CLI v2.1.195 on 2026-06-28.
