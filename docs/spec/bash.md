# Bash

## Purpose

Execute a shell command and return its output. Unlike the file tools, which are about state on disk, `Bash` is the agent's general-purpose escape hatch for actions the more specific tools do not cover: running build commands, spawning long-lived processes, invoking system utilities, and so on.

`Bash` is also the most semantically complex of the built-in tools. The upstream contract pins down behaviour around timeouts, output truncation, working-directory persistence, environment, and background tasks. Each of those is a design decision that this server has to make explicit.

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
        "description": "Per-command timeout in milliseconds. Capped by the implementation."
      },
      "run_in_background": {
        "type": "boolean",
        "description": "When `true`, the command runs as a background task instead of blocking the tool call.",
        "default": false
      }
    },
    "required": ["command"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names match the names the model sees in the prompt-level descriptions.

## Semantics

### Process model

- Each `bash` call runs in a **separate child process**. The shell state itself (`export`-ed environment variables, shell functions defined inline, current shell options) does **not** persist from one call to the next.

### Working directory

The upstream tool keeps `cd` across calls when the resulting directory is inside the project (or one of the additional working directories configured for the session). If `cd` would land outside that boundary, the directory is reset to the project root and the tool appends a notice to the result describing the reset.

How this server preserves working directory across calls is a design decision recorded in [Known gaps](#known-gaps). Two implementations are reasonable:

- **No persistence**: every call starts in the server's startup working directory. Simpler, predictable, and matches the natural behaviour of `exec` for a one-shot child process. Forces the caller to use absolute paths or chained `cd` within a single call.
- **Persistent within a session**: the server tracks the working directory across calls in some way (a sentinel command appended to detect the final cwd, or a parser that interprets the leading `cd`). Closer to the upstream behaviour, more code to maintain, and surface area for subtle bugs.

### Environment

- The child process inherits the server's environment by default.
- `export` in one call has no effect on the next. Persistent environment changes are not part of the tool's contract.

### Timeouts

- The default per-command timeout matches the upstream value: **120 000 ms** (two minutes).
- The maximum per-command timeout the caller can request matches the upstream cap: **600 000 ms** (ten minutes).
- A timeout produces an error result rather than a successful empty output, so the caller can distinguish "command produced nothing" from "command was killed".
- The defaults and cap are exposed for configuration; values are documented in the project README.

### Output

- The upstream tool truncates command output at a configurable default and writes the full output to a session-local file when it overflows. The default cap upstream is **30 000 characters** with a configurable hard ceiling of **150 000 characters**.
- This server's default cap, and whether it truncates or fails when the cap is exceeded, is a design decision recorded in [Known gaps](#known-gaps). The implementation will preserve the caller's ability to *keep getting useful results from large outputs*, even if the mechanism differs from the upstream "save to file and hand back a path".
- Standard output and standard error are returned together in the order they were produced. The exit code is included in the result.

### Background tasks

- `run_in_background: true` starts the command and returns immediately with a handle the caller can use to read incremental output and to stop the task. This is the right mode for long-running processes such as dev servers and watchers.
- The exact handle format (id, file path, …) and the companion operations (read more output, stop the task) are part of the implementation contract and will be documented alongside the implementation.

## Edge cases and constraints

- An empty `command` is an error.
- A `timeout` larger than the cap is clamped to the cap (or rejected — the implementation will pick one and record the choice).
- Killing a backgrounded child process cleanly is OS-specific. The implementation should make a best-effort attempt to terminate the entire process group rather than only the immediate child, and document the limitations.
- On non-POSIX hosts (Windows without WSL), the behaviour is host-dependent. The upstream CLI falls back to PowerShell where Git Bash is unavailable; this server documents which shells it expects to be present on the host.

## Error behaviour

Concrete error string formats are **TBD**. The implementation will distinguish:

- empty command,
- timeout (with the requested timeout and the actual elapsed time),
- non-zero exit code (with the code and a preview of the output),
- output-cap exceeded (with the actual length and the cap value),
- failures to spawn the process (with the OS-level error),
- background-task lifecycle errors (handle not found, already terminated, …).

The exit code is always part of the success result, not folded into the error path: a non-zero exit code that the caller asked for (`test 1 -eq 2`, `grep` with no matches, …) is not a tool-level error.

## Permissions and security

- The upstream `Bash` tool requires a permission prompt by default. Allow/deny rules use the `Bash(<command-pattern>)` form, with a rich pattern language that understands chained commands, process wrappers (`timeout`, `time`, `nice`, `nohup`, `stdbuf`, bare `xargs`), and a fixed set of read-only commands that bypass the prompt.
- The upstream CLI optionally runs the shell command inside an OS-level sandbox (Seatbelt on macOS, bubblewrap on Linux/WSL2). The sandbox is orthogonal to the permission rules and isolates only the shell subprocess.
- This MCP server is **sandbox-agnostic**. It does not implement permission patterns, does not maintain an allow-list of read-only commands, and does not wrap the shell in a sandbox. The hosting environment is expected to provide both. Concretely: the server should be deployed inside whatever container/microVM/VM the hosting environment chooses, and the caller-side permission policy (if any) should be enforced by the agent harness — not by this server.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Working-directory persistence.** The upstream tool persists `cd` across calls within a project boundary. This server will pick one of: no persistence (simpler, predictable), or persistence implemented through one specific mechanism. The choice is recorded here at implementation time.
- **Output cap policy.** The upstream tool truncates and offloads overflow to a file. This server will pick a policy (truncate-in-place with a notice, fail on overflow, or offload to a file path) and record the choice here.
- **Background task control surface.** The upstream tool exposes background tasks through specific affordances (`/tasks`, a dedicated `Monitor` tool). This server will need an equivalent for reading incremental output and stopping a task, and the exact shape (one MCP tool with subcommands, multiple MCP tools, …) is open.
- **Shell choice on Windows.** The upstream CLI prefers Git Bash and falls back to PowerShell. This server will document its expectations (POSIX shell available on `PATH`, or a specific name) rather than reproducing the fallback.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Bash tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Bash section, read-only commands), [`sandboxing`](https://code.claude.com/docs/en/sandboxing), [`security`](https://code.claude.com/docs/en/security), [`env-vars`](https://code.claude.com/docs/en/env-vars).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: the `tool-description-bash-*` cluster (overview, timeout, working-directory, maintain-cwd, output-truncation, dedicated-tools-preference, sandbox-related variants), the `tool-parameter-bash-run_in_background-*` files, and `tool-description-background-monitor-streaming-events.md`.
- `1rgs/nanocode`: the `bash` tool implementation, used as an independent cross-check for the spawn-per-call model and the default-timeout shape.
