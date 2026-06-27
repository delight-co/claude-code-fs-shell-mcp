# Bash

## Purpose

Execute a shell command and return its output. Unlike the file tools, which operate on state on disk, `Bash` is the agent's general-purpose escape hatch for actions the more specific tools do not cover: running build commands, spawning long-lived processes, invoking system utilities, and so on.

`Bash` is the most semantically complex of the built-in tools. The upstream contract pins down behaviour around timeouts, output truncation, working-directory persistence, environment, background tasks, and a deep integration with the filesystem tools through which certain `read`-equivalent shell invocations seed the read-tracking state used by [`Write`](./write.md) and [`Edit`](./edit.md). Each of those is a design decision this MCP server has to make explicit.

## Signature

```json
{
  "name": "bash",
  "input_schema": {
    "type": "object",
    "properties": {
      "command": {
        "type": "string",
        "description": "The shell command to execute."
      },
      "description": {
        "type": "string",
        "description": "Short past-tense summary of what the command does (a few words). Surfaced in UI rows; the model is encouraged to provide one."
      },
      "timeout": {
        "type": "integer",
        "description": "Per-command timeout in milliseconds. Capped by the implementation (see Semantics / Timeouts)."
      },
      "run_in_background": {
        "type": "boolean",
        "description": "When `true`, the command runs as a background task instead of blocking the tool call.",
        "default": false
      },
      "dangerouslyDisableSandbox": {
        "type": "boolean",
        "description": "Bypass the sandbox for this single call. The hosting environment may still refuse the bypass by policy.",
        "default": false
      }
    },
    "required": ["command"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names mirror the names the model sees in the prompt-level descriptions. A `_simulatedSedEdit` internal field exists upstream as a `Bash`-to-`Edit` promotion shortcut; this MCP server does not implement that path — see [Known limitations](#known-limitations).

## Capability boundaries

`Bash` tool functionality falls into four capability tiers in this MCP server context. The Known gaps and Known limitations sections below use this taxonomy when describing each behaviour's status.

| Tier | What it covers | Status in this server |
| ---- | -------------- | --------------------- |
| **1 — Self-contained** | Implementable without cross-tool or harness coordination (shell spawn, env construction, timeout, kill, output cap, sandbox wrapping). | **In scope for the initial implementation.** |
| **2 — fs-tool integration** | Implementable through the per-session state model already used by [`Read`](./read.md) / [`Write`](./write.md) / [`Edit`](./edit.md). Closes the [`Write`](./write.md) and [`Edit`](./edit.md) specs' "Read-equivalent shell commands" known gap. | **In scope for the initial implementation.** |
| **3 — Sibling-tool integration** | Implementable in principle but depends on the background-task tool family ([`TaskOutput`](./task-output.md), [`TaskStop`](./task-stop.md), [`TaskList`](./task-list.md), [`TaskGet`](./task-get.md), [`Monitor`](./monitor.md)) being exposed. | **Deferred** to a follow-up milestone; the `run_in_background: true` immediate-return contract is partially specified here so the eventual handoff is unambiguous. |
| **4a — Architecturally infeasible** | Behaviours that depend on internal CLI state or in-process hooks not reachable through MCP. | **Not reproducible**; recorded in Known limitations so callers know the gap. |
| **4b — Implementable but deferred** | The MCP server could maintain its own equivalent state but the initial milestone does not prioritise it. | **Deferred**; recorded in Known gaps with a note that the gap is intentional, not architectural. |

## Semantics

### Process model

Each `bash` call runs in a **separate child process**. The shell state itself (`export`-ed environment variables, shell functions defined inline, current shell options) does **not** persist from one call to the next.

The shell binary is selected by the following preference order:

1. `CLAUDE_CODE_SHELL` env var if it is set and contains `bash` or `zsh`.
2. `SHELL` env var if it is set and contains `bash` or `zsh`.
3. The first match found in `/bin`, `/usr/bin`, `/usr/local/bin`, `/opt/homebrew/bin` for `bash` or `zsh` in that preference.

If no suitable shell is found, the tool fails with the wording given under [Errors](#errors).

The child process is started **detached** (in its own process group on POSIX) so the kill strategy below can address the whole tree.

### Shell snapshot

To make the first `bash` call usable without requiring the agent to re-export aliases, functions, and `PATH` entries that the user has in their shell startup files, the server captures a **shell snapshot** at server startup (alias / function / env / `PATH` state of a login shell). Each `bash` call then `source`s this snapshot before evaluating the user command.

The snapshot is taken once at startup. Dynamic state changes (`export FOO=...`, `alias bar=...`) that the user command performs do **not** persist to subsequent calls — the snapshot is not re-captured per call.

### Working directory persistence

The upstream tool keeps `cd` across calls when the resulting directory is inside the project (or one of the additional working directories configured for the session). This MCP server reproduces the same model: a `pwd -P >| <per-call-file>` sentinel is appended to every command, and the post-exit hook reads the file to record the new cwd in per-session state.

#### Project boundary

The set of directories considered "inside the project" is the union of:

- The server's startup working directory (treated as the original cwd).
- Any additional working directories registered for the session through the server's configuration surface (the mechanism mirrors the upstream `--add-dir` option; the server exposes it through an environment variable, documented in the project README at implementation time).

Boundary comparisons resolve symlinks on both sides and apply the macOS `/private/var` → `/var/`, `/private/tmp` → `/tmp` normalisation before checking containment.

#### Reset on boundary violation

If a command's resulting cwd is **outside** the project boundary (or if `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR` is set and any cwd change occurred), the cwd is reset to the original startup cwd before the result is returned to the caller, and the following notice is appended to the result (verbatim, leading newline included):

```
Shell cwd was reset to <new cwd>
```

#### Missing cwd recovery

If the cwd persisted from a previous call no longer exists on disk at the start of the next call, the server attempts to recover to the nearest existing ancestor. The wording emitted in each branch is given under [Errors](#errors).

### Environment

The child process inherits the server's environment by default. The following overrides are layered on top before each spawn:

- `SHELL` is set to the detected shell path.
- `GIT_EDITOR` is set to `"true"` so interactive git editor invocations terminate immediately.
- If a sandbox temporary directory has been provisioned for the call, `TMPDIR` and related vars are pointed at it.
- A small set of credentials and OpenTelemetry variables that exist in the server process for its own use (LLM API tokens, OTLP exporters) are **stripped** before the spawn so the child cannot observe them. The exact list is documented in the project README.

`export` performed inside one call has no effect on the next, per [Process model](#process-model).

### Timeouts

- The default per-command timeout matches the upstream value: **120 000 ms** (two minutes).
- The maximum per-command timeout the caller can request matches the upstream cap: **600 000 ms** (ten minutes).
- A `timeout` larger than the cap is **clamped** to the cap (not rejected); a `timeout` of 0 or negative is treated as unset and falls back to the default.
- Both values are overridable through the environment variables `BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS`. The cap is constrained to be at least the default (`max(env-cap, default)`).
- A timeout produces an error result with the wording given under [Errors](#errors); standard output captured before the timeout is preserved in the result.

### Kill strategy

When the call needs to terminate the child process (timeout, abort signal, explicit [`TaskStop`](./task-stop.md) against a backgrounded task), the strategy is:

1. Send `SIGTERM` to the **process group** (`process.kill(-pid, "SIGTERM")`).
2. Enumerate descendant PIDs via `ps -A -o pid= -o ppid=` and send `SIGTERM` to each.
3. Wait **1500 ms** for the process tree to exit.
4. If the tree is still alive, send `SIGKILL` to the process group, then `SIGKILL` to each descendant.
5. Poll `process.kill(pid, 0)` every **100 ms** to confirm exit.

An `abort` signal whose reason is `interrupt` (the user pressed `Ctrl+C` at the agent's UI) skips the kill above; that case is handled by the upstream interrupt path and surfaces under a different result shape.

### Output handling

The upstream tool truncates and offloads large output through a three-tier cap chain. This MCP server reproduces all three tiers.

| Tier | Threshold | Trigger | Behaviour |
| ---- | --------- | ------- | --------- |
| In-memory buffer per call | 8 MiB (`8 388 608` bytes) | Per-call memory cap | Output above the cap spills to a file in the session's tool-results cache directory. |
| File-spill hard cap | 5 GiB (`5 368 709 120` bytes) | Per-call file cap | The command is killed and the result carries the wording `Command killed: output file exceeded 5GB`. |
| Tool-result wrap cap | `30 000` characters by default, configurable up to `150 000` via `BASH_MAX_OUTPUT_LENGTH` | Per-call tool-result cap | The full output is persisted to the cache directory; the tool result carries a `<persisted-output>` wrapper with a preview of the first 2 000 characters. |

Persisted files live at `<session-cache-dir>/<sessionId>/tool-results/<taskId>.txt` (the exact root is configured at server startup; the project README documents it).

Standard output and standard error are returned together. In the in-memory tier they are tracked separately and surfaced separately; once a call has spilled to a file, the two streams are interleaved in time order with stderr lines prefixed by `[stderr] `.

The final tool result content concatenates, in order: stdout, stderr, the optional backgrounded-task notice (see [Background tasks](#background-tasks)), the optional stale-read-state hint (see [Notices](#notices)), and the optional gh-rate-limit hint.

The notice and error wordings emitted in this section are pinned verbatim under [Errors](#errors) and [Notices](#notices).

### Background tasks

`run_in_background: true` starts the command and returns **immediately** with the result:

```
{ "stdout": "", "stderr": "", "code": 0, "interrupted": false, "backgroundTaskId": "<taskId>" }
```

The command continues to run; its output continues to accumulate in its output file at `<session-cache-dir>/<sessionId>/tool-results/<taskId>.output`. The `backgroundTaskId` is a short identifier the agent uses with the sibling tools to read incremental output, stop the task, or list the tasks the session owns.

Two notice wordings accompany the result depending on whether the background was requested by the model itself or attached by the user; both are given under [Notices](#notices).

#### Sibling tool family

The background-task surface is completed by five sibling tools that this server's spec captures separately:

- [`TaskOutput`](./task-output.md) — fetch the output of a task by id.
- [`TaskStop`](./task-stop.md) — terminate a task by id.
- [`TaskList`](./task-list.md) — list the tasks tracked by the session.
- [`TaskGet`](./task-get.md) — fetch the registry entry for a task by id.
- [`Monitor`](./monitor.md) — observe a shell or WebSocket stream and notify the agent on each event.

The sibling tools are **deferred** out of the initial implementation milestones (capability tier 3). The `run_in_background: true` path itself is specified here for completeness; whether the initial `Bash` implementation accepts the flag at all, rejects it with an error, or accepts it but ignores it (silently forcing foreground execution) is recorded under [Known gaps](#known-gaps).

#### Auto-backgrounding

The upstream tool optionally promotes a long-running foreground command to a background task once it has been running for `2 000 ms`, provided the command shape is eligible (single command, not a pure `sleep`, etc.). This MCP server's implementation will adopt the same threshold but the eligibility predicate is recorded as a [Known gap](#known-gaps).

#### Per-task time cap

Background tasks started by a sub-agent (an agent invocation with a non-empty `agentId`) are capped at **1 hour** by default, overridable through `CLAUDE_SUBAGENT_BG_SHELL_MAX_MS`. Background tasks started by the main thread are **not** time-capped upstream; the operating system's memory-pressure signal is the only automatic reaper. The MCP server follows the same policy.

### Read-equivalent shell commands

To close the "Read-equivalent shell commands" known gap on the [`Write`](./write.md) and [`Edit`](./edit.md) specs, the server inspects each successful `Bash` command and seeds the per-session read-tracking state when the command is recognisable as a single-file read.

The recognised forms are (each shown without the `<file>` argument):

- `cat`, `cat -n`, `cat --number`
- `nl`
- `bat`, `bat -n`, `bat --number`, `bat -p`, `bat --plain`
- `batcat`, `batcat -n`, `batcat --number`, `batcat -p`, `batcat --plain`
- `head [-n N | -N | --lines=N]` (defaults to 10 lines)
- `tail [-n N | -N | --lines=N]` (defaults to 10 lines)
- `sed -n 'Np' <file>` and `sed -n 'N,Mp' <file>` (rejects `-i`, `--in-place`, `-e`)
- `grep [safe flags] <pattern> <file>` and `egrep` / `fgrep` aliases — single command only, requires exit status 0
- `rg [safe flags] <pattern> <file>` — single command only, requires exit status 0

Multi-command sequences (`;`, `&&`, `||`) are honoured if each sub-command is one of the forms above, with two exceptions:

- `grep` / `egrep` / `fgrep` / `rg` are only recognised when the **whole command** is a single command (they are not honoured inside multi-command sequences).
- Filler commands `echo`, `printf`, `true`, and `:` are tolerated inside multi-command sequences without preventing the recognition of the other parts.

The recognition is bypassed entirely (no seeding happens) when:

- The command contains a shell pipe (`|`), input redirect (`<`), or output redirect (`>`).
- The file size exceeds **10 MiB** (`10 485 760` bytes).
- The command was interrupted, returned a non-zero exit for the `grep` / `rg` cases, or was started as a background task.

A successful seed records the file's content, current `mtime`, and (for range forms) the `offset` / `limit` window the recognised command read. Subsequent [`Write`](./write.md) / [`Edit`](./edit.md) calls against the same path see this seed as if the agent had called [`Read`](./read.md) explicitly.

### Sandbox integration

The upstream CLI optionally runs the shell command inside an OS-level sandbox (Seatbelt on macOS, `bubblewrap` on Linux/WSL2). The sandbox composes with — does not replace — the permission rules and isolates only the shell subprocess.

This MCP server is **sandbox-agnostic**. It does not implement permission patterns, does not maintain an allow-list of read-only commands, and does not wrap the shell in a sandbox itself. The hosting environment (the container, microVM, or VM in which the server is deployed) is expected to provide both. The `dangerouslyDisableSandbox` parameter is accepted by the schema for upstream-signature parity but has **no effect** in this server's implementation — it is the hosting environment's job to decide whether a sandbox runs, not this server's.

## Errors

Errors are surfaced as MCP tool errors. The exact wording is pinned below for each condition. Where the wording embeds a runtime value, the value appears in angle brackets (`<like-this>`).

### Pre-flight (validateInput)

- **Empty / control-character command** — caught at schema level. The schema rejects commands containing characters that would be hidden in an approval dialog. The exact MCP error format depends on the server's schema runtime.
- **Sleep block** (optional, gated on the upstream `tengu_amber_sentinel` experiment flag, default off):
  ```
  Blocked: <reason>. To wait for a condition, use Monitor with an until-loop (e.g. `until <check>; do sleep 2; done`). To wait for a command you started, use run_in_background: true. Do not chain shorter sleeps to work around this block.
  ```
  The MCP server does not implement this gate in the initial milestone (capability tier 4b).

### Permission (checkPermissions)

- **Background operator (`&`) safety check**:
  ```
  This command uses the `&` background operator, which defers execution past approval-time safety checks. Approve only if you trust it.
  ```
  This MCP server does not run a permission system of its own; the hosting environment is expected to apply this check if it cares about the same safety property.
- **Sandbox bypass attempted**:
  ```
  Run outside of the sandbox
  ```
  Emitted by the upstream tool when `dangerouslyDisableSandbox: true` is supplied against a command the policy would otherwise sandbox. Not emitted by this server (sandbox-agnostic).

### Call

- **Shell not found**:
  ```
  No suitable shell found. Claude CLI requires a Posix shell environment. Please ensure you have a valid shell installed and the SHELL environment variable set.
  ```
- **Pre-spawn error** (null byte in command, cwd unavailable, etc.):
  - Null bytes — `Bash: command contained null bytes (argv echo redacted)`
  - Other pre-spawn errors — `Bash: pre-spawn error (cwd/argv redacted)`
- **Cwd missing at call time**:
  - Without recovery — `Working directory "<path>" no longer exists. Please restart Claude from an existing directory.`
  - With recovery to nearest existing ancestor — `Working directory "<oldPath>" was deleted; shell cwd recovered to "<newPath>". Re-issue your command (it will run from the recovered directory).`
- **Cancellation (call interrupted before execution)** — surfaced in `stderr`:
  ```
  Command aborted before execution
  ```
- **Cancellation (call interrupted after execution started)** — appended to `stderr`:
  ```
  <error>Command was aborted before completion</error>
  ```
- **Non-zero exit code** — appended to `stderr`:
  ```
  Exit code <code>
  ```
- **Timeout** — appended to `stderr`:
  ```
  Command timed out after <duration>
  ```
  where `<duration>` is a human-readable formatting of the timeout (`2m`, `30s`, ...).
- **Output file cap exceeded (5 GB)** — appended to either `stdout` (when the call started in file mode) or `stderr` (when the call spilled mid-run):
  ```
  Command killed: output file exceeded 5GB
  ```
- **Output file unavailable** (for file-mode reads where the file has been removed by another process):
  ```
  <bash output unavailable: output file <path> could not be read (<errno>). This usually means another Claude Code process in the same project deleted it during startup cleanup.>
  ```

## Notices

Notices are not errors. They are appended to the tool result alongside the command's output and (in the upstream tool) are recognised as system-level annotations rather than command output.

- **Cwd reset on boundary violation** — appended to `stderr` with a leading newline:
  ```

  Shell cwd was reset to <cwd>
  ```
- **Output truncated to file** (spill-after-memory mode) — appended:
  ```

  Output truncated (<KB>KB total). Full output saved to: <path>
  ```
- **Output persisted (tool-result wrap)** — replaces the body when the tool result would exceed the cap:
  ```
  <persisted-output>
  Output too large (<size>). Full output saved to: <path>

  Preview (first <preview-size>):
  <preview body>
  ...
  </persisted-output>
  ```
  The trailing `...\n` is omitted when the preview is the entire output.
- **Background task started (auto-backgrounded by the tool)**:
  ```
  Command running in background with ID: <taskId>. Output is being written to: <path>. You will be notified when it completes. To check interim output, use Read on that file path.
  ```
- **Background task started (manually backgrounded by the user)**:
  ```
  Command was manually backgrounded by user with ID: <taskId>. Output is being written to: <path>
  ```
- **Stale read-state hint** — appended when the command was a recognised write-shaped command (formatters, `--fix` shapes, in-place edits) and it modified files the agent had previously read in the same session:
  ```
  [This command modified <N> file/files you've previously read: <paths>. Call Read before editing.]
  ```
  The singular/plural form follows English grammar and the path list is truncated with ` and <X> more` past five entries.

## Permissions and security

- The upstream `Bash` tool requires a permission prompt by default. Allow/deny rules use the `Bash(<command-pattern>)` form. A multi-layered "auto-allow" set is maintained for read-only commands that bypass the prompt (`cat`, `ls`, `head`, `tail`, etc.) and a separate set of process wrappers (`timeout`, `time`, `nice`, `stdbuf`, `nohup`, `command`, `builtin`, `noglob`) is automatically stripped before the read-only check runs.
- The upstream CLI optionally runs the command inside `sandbox-exec` (macOS) or `bubblewrap` (Linux/WSL2), and skips the sandbox for commands matching `settings.sandbox.excludedCommands`.
- This MCP server is **sandbox-agnostic**: it does not implement the permission patterns, does not maintain a read-only command list, and does not wrap the shell in an OS-level sandbox. The hosting environment is expected to provide both. Concretely: deploy the server inside whatever container / microVM / VM the hosting environment chooses, and let the agent harness apply caller-side permission policy.
- See [Common conventions](./README.md#common-conventions) for the cross-cutting response-shape rule.

## Implementation status

🟢 **Drafted.** Spec essentially complete from public sources and verification against the pinned CLI version; remaining open points (see [Known gaps](#known-gaps)) will be resolved in the implementation pull request. Implementation has not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

These are gaps the implementation pull request will close, either by choosing a concrete behaviour or by reporting observed behaviour against the pinned CLI version.

- **(Tier 3) `run_in_background: true` initial scope.** The immediate-return contract and the `backgroundTaskId` shape are specified above, but the sibling tools that make a background task useful ([`TaskOutput`](./task-output.md), [`TaskStop`](./task-stop.md), etc.) are deferred. The initial `Bash` implementation will pick one of:
  - reject the flag with a clear error pointing at the deferral,
  - accept the flag but force foreground execution and emit a notice that says so, or
  - accept the flag, start the task in the background, and rely on a later sibling-tools PR to make the task addressable.

  The choice will be made and recorded here at impl time.
- **(Tier 3) Auto-background eligibility.** Whether a long-running foreground command should be auto-promoted to a background task at `2 000 ms` depends on a command-shape predicate that the upstream applies. The initial implementation will adopt the threshold and document the predicate.
- **(Tier 3) `Monitor` integration.** Out of initial scope. The [`Monitor`](./monitor.md) skeleton spec records the surface; implementation lands with the rest of the background-task family.
- **(Tier 4b) Sleep-block gate.** The upstream `tengu_amber_sentinel` flag, when on, blocks `sleep N` (`N ≥ 25` seconds) outside of `Monitor` patterns. The MCP server could implement an equivalent flag-gated guard but does not in the initial milestone.
- **(Tier 4b) gh rate-limit hint.** The upstream tool tracks gh-CLI rate-limit state across calls and appends a hint when an out-of-quota response is detected. This MCP server could track the same state but does not in the initial milestone.
- **(Tier 4b) git-operation classification.** The upstream tool classifies commit / push / merge / rebase / PR operations to apply specific safety prompts. This MCP server could implement an equivalent classifier but does not in the initial milestone.
- **(Tier 4b) Personal- and team-memory secret scanning.** The upstream CLI runs a secret scanner against commands that target its memory directories. The MCP server has no concept of those directories and does not scan; this could be implemented as an opt-in plug-in but is not part of the initial milestone.
- **Shell choice on Windows.** The upstream CLI prefers Git Bash and falls back to PowerShell. This server will document its expectations on the hosting environment (POSIX shell on `PATH`) rather than reproducing the fallback.

## Known limitations

These behaviours of the upstream Claude Code CLI's built-in `Bash` cannot be reproduced by an MCP server interposed between the CLI and the shell. They are recorded here so callers know which built-in behaviours are not available through this server.

- **(Tier 4a) PostToolUse hook re-sync.** After a successful `Bash` call the upstream CLI runs PostToolUse hooks (formatters, linters configured by the user) and re-reads any files whose `mtime` advanced as a result, refreshing its read-tracking state with the post-hook content. The MCP server has no in-CLI hook surface and cannot drive this. Callers running formatters as PostToolUse hooks need to issue an explicit [`Read`](./read.md) after each `Bash` call that may have triggered such a hook.
- **(Tier 4a) `_simulatedSedEdit` promotion.** The upstream CLI inspects `Bash` calls whose command is a `sed` invocation matching an edit shape and rewrites the call internally as an [`Edit`](./edit.md) tool invocation. This crosses the boundary between the CLI's tool dispatcher and individual tool implementations — there is no MCP-level surface for the server to drive an equivalent rewrite. Callers wanting an in-place edit should call [`Edit`](./edit.md) directly instead of `sed -i` / `sed -i ''`.
- **(Tier 4a) Background-session worktree isolation.** The upstream CLI maintains an isolation invariant that prevents a background session from issuing `Bash` calls that touch a different worktree. The MCP server has no concept of background sessions or multiple worktrees within a session and does not enforce this.
- **(Tier 4a) Cross-tool task registry sharing.** The upstream task registry that `run_in_background: true` writes to is shared with other in-CLI tools (the `Monitor` tool, the agent invocation surface, the UI's `/tasks` slash command). The MCP server's task registry, when it lands, is **per-session** and not shared with anything outside this server's process; tasks started here are not visible to upstream tools running outside the server.
- **(Tier 4a) Cwd shared with other in-CLI tools.** The upstream CLI stores the persisted cwd in an in-process value other tools running in the same agent turn can read. The MCP server's per-session cwd is not exposed to anything outside this server; cwd persistence is therefore scoped to subsequent `Bash` calls within the same MCP session.
- **(Tier 4a) Experiment-flag escape hatches.** The upstream CLI exposes experiment flags that, when enabled, bypass the `&` safety check or the cwd reset. The MCP server does not implement these escape hatches; the same overrides can be achieved at the hosting-environment level if a deployment needs them.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/):
  - [`tools-reference`](https://code.claude.com/docs/en/tools-reference) — Bash tool behavior section.
  - [`permissions`](https://code.claude.com/docs/en/permissions) — Bash section, read-only commands.
  - [`sandboxing`](https://code.claude.com/docs/en/sandboxing).
  - [`security`](https://code.claude.com/docs/en/security).
  - [`env-vars`](https://code.claude.com/docs/en/env-vars).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0` (Claude Code CLI v2.1.191, 2026-06-24): the `tool-description-bash-*` cluster (overview, timeout, working-directory, maintain-cwd, output-truncation, dedicated-tools-preference, sandbox-related variants), the `tool-parameter-bash-run_in_background-*` files, and `tool-description-background-monitor-streaming-events.md`.
- [`1rgs/nanocode`](https://github.com/1rgs/nanocode): the `bash` tool implementation, used as an independent cross-check for the spawn-per-call model and the default-timeout shape.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
