# Glob

## Purpose

Find files by name pattern. Where [`Grep`](./grep.md) looks inside files, `Glob` looks at the directory tree and returns paths that match a glob expression.

The search engine is **ripgrep** in `--files` mode: the wrapper enumerates files via `rg --files` with the glob applied as a filter. Glob shares the same ripgrep binary resolution, the same child-process driver, and the same result wrapper with [`Grep`](./grep.md); the differences are at the argument-assembly and the result-formatting layers.

## Signature

```json
{
  "name": "glob",
  "input_schema": {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "The glob pattern to match files against"
      },
      "path": {
        "type": "string",
        "description": "The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided."
      }
    },
    "required": ["pattern"]
  }
}
```

The schema mirrors the upstream `Glob` tool's two-parameter shape exactly. Unknown keys are rejected.

## Capability boundaries

| Tier | What it covers | Status in this server |
| ---- | -------------- | --------------------- |
| **1 — Self-contained** | ripgrep binary resolution, args assembly, output formatting, timeout, byte-exact error / notice wording, wrap cap, truncation notice. | **In scope for the initial implementation.** |
| **2 — fs-tool integration** | (Glob does not seed the read-tracking state; not applicable.) | n/a |
| **3 — Sibling-tool integration** | (Glob does not depend on other tool families; not applicable.) | n/a |
| **4a — Architecturally infeasible** | PostToolUse hooks (memory-dir tracking after a Glob call), REPL-inner native timeout, `Fpt()` removal-when-shell-available behaviour, embedded-ripgrep re-exec. | **Not reproducible** (Known limitations) |
| **4b — Implementable but deferred** | Orphaned-worktree exclusions, deny-rule path → `--glob !` conversion, pattern-side UNC / `/net` automount defensive ask, model-conditional `prompt()`, first-use availability check, REPL surface's 25 000-cap override, absolute-pattern silent search-root override reconciliation. | **Deferred** (Known gaps) |

## Semantics

### Search engine

The engine is **ripgrep** (`rg`) in `--files` mode. The MCP server expects `rg` on the hosting environment's `PATH`. Shared with [`Grep`](./grep.md) — same binary resolver, same child-process driver, same first-use availability check (recorded as Known gap; the implementation may add it later).

### Default args (always passed)

For every Glob call the wrapper passes:

- `--files` — rg's "list every file the matcher would walk" mode.
- `--glob <pattern>` — the (possibly rewritten, see Absolute-pattern handling) glob filter.
- `--sort=modified` — sort by mtime. Per the ripgrep manual, the unsuffixed `--sort=<key>` form is **ascending** (oldest first); `--sortr=` would be descending. The upstream passes `--sort=modified` verbatim (oldest first); the description text "sorted by modification time" leaves the direction unspecified.
- `--no-ignore` — `.gitignore` (and other ignore files) are **not respected** by default. Disabled only when `CLAUDE_CODE_GLOB_NO_IGNORE` env is explicitly set to a falsy value.
- `--hidden` — dotfiles are included. Disabled only when `CLAUDE_CODE_GLOB_HIDDEN` env is explicitly set to a falsy value.

Notably **not** passed (in contrast to Grep):

- The six fixed VCS-dir exclusions (`!.git`, `!.svn`, etc.). With `--no-ignore`, even `.git/` files are walked unless excluded by other means.
- `--max-columns 500` (output mode is path-only, no line content).

### Absolute-pattern handling

When the supplied `pattern` is absolute (e.g., `/foo/**/*.go`), the wrapper extracts a static-prefix `baseDir` and a `relativePattern`, then **silently overrides** the search root to `baseDir` and uses `relativePattern` as the `--glob` value. The caller-supplied `path` is ignored in this branch.

This matches the upstream behaviour exactly. A subtle consequence: the **permission check** (via `getPath`) still sees the caller-supplied `path` (or cwd fallback), while the actual rg search runs at the extracted `baseDir`. The MCP server reproduces the same mismatch for parity; see Known gaps for a note on whether to reconcile.

### Pagination / cap

- `globLimits.maxResults` default is **100**. The upstream REPL surface overrides this to **25 000** for direct REPL invocations; the MCP server uses the lower 100 by default and treats the higher cap as a Known gap (configurable opt-in).
- `offset` is **hard-coded to 0** in the upstream caller; pagination knobs are not exposed on the tool itself.
- When the rg result count exceeds the cap, the result is sliced to the first `maxResults` entries and `truncated` is set to `true` (rendered as a notice — see Errors and notices).

### Timeout

- Default `20 000 ms`, increased to `60 000 ms` on WSL.
- Overridable via `CLAUDE_CODE_GLOB_TIMEOUT_SECONDS` env. (Shared with [`Grep`](./grep.md); there is no separate `CLAUDE_CODE_GREP_TIMEOUT_SECONDS` knob upstream.)
- A timeout sends `SIGTERM` to the rg child, then `SIGKILL` 5 seconds later.

### Output formatting

The wrapped tool-result content is plain text:

- Empty result (`filenames.length === 0`): `No files found`.
- Non-empty, not truncated: `<path>\n<path>\n...` (one path per line, no header, no trailing notice).
- Non-empty, truncated: `<path>\n<path>\n...\n(Showing N of M matching files; K more are not listed. Narrow the pattern or path to see the rest.)` where `N` = files in result, `M` = `totalMatches`, `K` = `M - N`.

Paths are absolute when outside cwd; cwd-relative when inside cwd (per the shared relativizer).

### Wrap cap

The upstream tool's `maxResultSizeChars` is `100000`, but the cap is further tightened to `min(100000, 50000) = 50000` characters by the wrap pipeline. When the assembled content exceeds 50 000 chars, the same `<persisted-output>` envelope used by other tools replaces the content:

```
<persisted-output>
Output too large (<size>). Full output saved to: <path>

Preview (first 2.00KB):
<first ~2000 chars>
...
</persisted-output>
```

The trailing `...\n` is omitted when the preview equals the entire output. When the rendered content is empty, the wrap pipeline substitutes `(Glob completed with no output)`.

This MCP server's initial implementation uses the same simplified single-tier truncate notice as Bash and Grep (the full `<persisted-output>` envelope is recorded as a Known gap shared across the three tools).

## Errors and notices

Errors are surfaced as MCP tool errors, wrapped by the tool runner as `<tool_use_error>...</tool_use_error>`. Wording is pinned below.

### `validateInput`

- **`path` does not exist** (`errorCode: 1`):
  ```
  Directory does not exist: <path>. Note: your current working directory is <cwd>.
  ```
  When a likely candidate directory exists nearby, a suffix is appended:
  ```
   Did you mean <suggested>?
  ```
  (Note: the wording uses "Directory does not exist" — distinct from Grep's "Path does not exist".)

- **`path` resolves to a non-directory** (file, symlink to file, etc.) (`errorCode: 2`):
  ```
  Path is not a directory: <path>
  ```
  (Glob rejects file paths; Grep accepts them — this is a deliberate upstream asymmetry.)

- **UNC paths (`\\…` / `//…`)** bypass the stat check and pass validation unchanged.

- **Null bytes**: Glob's `validateInput` does **not** check for null bytes in `pattern` (Grep does). A null byte in `path` is intercepted by the path normaliser and surfaces as a generic `Path contains null bytes` error.

### Permission-side defensive checks (pattern-side UNC / `/net` automount)

When the **`pattern`** itself (not just the resolved path) is a UNC pattern or under the `/net` automount map, the upstream raises an `ask` permission prompt:

- UNC pattern: `Claude requested permissions to glob <pattern>, which appears to be a UNC pattern that could access network resources.`
- `/net` automount: `Claude requested permissions to glob <pattern>, which is under the /net automount map and could trigger a DNS lookup and NFS mount to a remote host.`

These are upstream-specific defensive checks; the MCP server is sandbox-agnostic and does not run them. Recorded as Known gap.

### Spawn / runtime errors (shared with Grep)

These come from the shared rg result wrapper:

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

- **Invalid args / bad glob (rg exit code 2)**: **silently suppressed**. The wrapper resolves with empty results and the user sees `No files found`. This matches upstream behaviour.

- **Permission errors (`EACCES` / `EPERM`)**: propagated as the underlying spawn error message wrapped by `<tool_use_error>Error: ...</tool_use_error>`.

### Result-rendering wordings

| Condition | Wording |
| --- | --- |
| empty result | `No files found` |
| non-truncated | `<path>\n<path>\n...` (one path per line, no header) |
| truncated, with `totalMatches` known | `<paths>\n(Showing N of M matching files; K more are not listed. Narrow the pattern or path to see the rest.)` |
| truncated, with `totalMatches === undefined` (only on results persisted by older CLI versions) | `<paths>\n(Results are truncated. Consider using a more specific path or pattern.)` |
| truncated, with `countIsComplete === false` (currently unreachable in v2.1.195; reserved for future use) | `<paths>\n(Showing the first N files; there are more than M matches. Narrow the pattern or path to see the rest.)` |

### Tool-removed hint (upstream-only behaviour)

When the upstream CLI removes Glob from the tool set (`Fpt()` returns the removal set in local-agent + POSIX-shell context), an attempted Glob call is hinted with one of:

- `. Glob is not available in this session — find files with \`find\` via the Bash tool instead.` (if Bash is in scope)
- `. Glob is not available in this context. Use one of the available tools instead.` (otherwise)

The MCP server cannot remove its own tools mid-session and always exposes Glob; these wordings are recorded for parity awareness only.

## Permissions and security

- The upstream `Glob` tool is classed as **read-only** (`isReadOnly() === true`) and as a search command. It does **not** require a permission prompt in the upstream's default permission model.
- Permission rules use the `Read(<path-pattern>)` family — the same rules that gate `Read` / `Grep`. The upstream walks the search root's ancestors and applies any matching Read-deny / Read-ask / Read-allow rule.
- `ruleContentField` is `"path"`: user-authored `Glob(<...>)` rules are interpreted as **path globs**, not as glob patterns themselves.
- Additionally, the upstream raises an `ask` permission prompt for `pattern`-side UNC / `/net` automount patterns (see Errors and notices above). The MCP server is sandbox-agnostic and does not run these defensive checks.
- The MCP server does not maintain a permission system of its own and does not wrap the rg child process in an OS-level sandbox. The hosting environment is expected to constrain which paths are reachable.

## Implementation status

🟢 **Drafted.** The spec is essentially complete from public sources and the v2.1.195 verification; the implementation will land in a follow-up PR. See [README](./README.md) for the project-wide matrix.

## Known gaps

These are gaps the implementation pull request will close, either by choosing a concrete behaviour or by reporting observed behaviour against the pinned CLI version.

- **(Tier 4b) Orphaned-worktree exclusions.** Upstream filters out paths under `.orphaned_at` markers in the CLI's cache directory. The MCP server has no equivalent cache model and skips this; in practice the exclusion is a no-op upstream too for project-tree searches.
- **(Tier 4b) Deny-rule path → `rg --glob !` conversion.** Upstream walks the `Read`-deny rules and converts each to a `--glob !` exclusion. The MCP server does not run a permission system of its own and skips this.
- **(Tier 4b) Pattern-side UNC / `/net` automount defensive ask.** Upstream raises an `ask` permission prompt for suspicious `pattern` values (UNC, `/net`). The MCP server is sandbox-agnostic and does not raise the prompt; the hosting environment is expected to filter such patterns externally if needed.
- **(Tier 4b) Model-conditional `prompt()`.** Upstream serves a compact prompt for non-Anthropic / unknown models. An MCP server exposes a single prompt per registration; the full form is always used.
- **(Tier 4b) First-use `rg --version` availability check.** Same Known gap as Grep.
- **(Tier 4b) REPL surface's 25 000-cap override.** Upstream raises the result cap to 25 000 when Glob is called from the REPL surface. The MCP server has no REPL surface and uses the lower 100 cap by default; making this configurable (env var or `GlobConfig`) is recorded as a follow-up.
- **(Tier 4b) `--sort=modified` direction confirmation.** Per the ripgrep manual the unsuffixed form is ascending (oldest first), which contradicts a literal reading of the upstream description ("sorted by modification time"). The implementation will adopt rg's ascending behaviour by passing the same flag verbatim and document the resulting order in the README. A follow-up may add a `--sortr=modified` (descending) option behind a config flag.
- **(Tier 4b) Wrap cap simplification.** Same Known gap as Bash and Grep: the initial impl uses a single-tier truncate notice instead of the full `<persisted-output>` envelope.
- **(Tier 4b) Absolute-pattern silent search-root override.** When `pattern` is absolute, the upstream silently overrides the caller's `path`. The permission check still sees the caller's `path`, producing a subtle mismatch. The MCP server's implementation will reproduce the same behaviour for parity; whether to surface a notice or reconcile the two paths is recorded for a follow-up.
- **Behaviour when `pattern === ""`.** rg's `--glob ""` returns exit code 2 (suppressed upstream), resulting in `No files found`. Behaviour confirmation by execution is queued.
- **Behaviour when `globLimits.maxResults` is 0 or negative.** The MCP server's implementation will clamp to a sensible default; the choice is recorded at impl time.

## Known limitations

These behaviours of the upstream Claude Code CLI's built-in `Glob` cannot be reproduced by an MCP server interposed between the CLI and the filesystem.

- **(Tier 4a) PostToolUse hooks.** Memory-directory tracking telemetry (`tengu_memdir_accessed` / `tengu_team_mem_accessed`) and any user-configured PostToolUse hooks run after every successful Glob call upstream; no MCP-level surface.
- **(Tier 4a) REPL-inner native timeout.** The upstream applies an additional `10 000 ms` REPL-inner wrapper timeout. An MCP server has no REPL layer to wrap.
- **(Tier 4a) `Fpt()` tool removal in local-agent + POSIX shell context.** Upstream may remove Glob from the tool set under specific conditions and substitute a hint ("find files with `find` via the Bash tool instead"). An MCP server cannot remove its own tools mid-session; Glob is always present.
- **(Tier 4a) Embedded ripgrep mode.** The upstream's Bun-bundled binary re-executes itself with `argv0=rg`. An MCP server is a standalone process and cannot re-exec a parent host binary; the server relies on `rg` being on `PATH`.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/):
  - [`tools-reference`](https://code.claude.com/docs/en/tools-reference) — Glob tool behavior section.
  - [`permissions`](https://code.claude.com/docs/en/permissions) — Read and Edit section (note: `Read(...)` rules apply to `Glob` on a best-effort basis).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0` (Claude Code CLI v2.1.191, 2026-06-24): `tool-description-glob.md`, `tool-description-glob-compact.md`.
- [`1rgs/nanocode`](https://github.com/1rgs/nanocode): the `glob` tool implementation, used as an independent cross-check for the mtime sort and the truncation behaviour.

Verified against Claude Code CLI v2.1.195 on 2026-06-28.
