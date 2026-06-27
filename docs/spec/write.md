# Write

## Purpose

Create a new file with given contents, or overwrite an existing file with given contents. `Write` is whole-file: it does not append, merge, or patch. Targeted in-place changes go through [`Edit`](./edit.md).

## Signature

```json
{
  "name": "write",
  "input_schema": {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "Absolute path to the file to write. Parent directories are created if missing."
      },
      "content": {
        "type": "string",
        "description": "The exact bytes to write to the file. Replaces the file entirely."
      }
    },
    "required": ["file_path", "content"]
  }
}
```

The upstream tool does not publish a JSON schema; the parameter names above match the names the model sees in the prompt-level descriptions and are stable across the captured CLI versions.

## Semantics

### Path handling

- `file_path` must be an absolute path. Relative paths are rejected without writing anything.
- Missing parent directories are **created recursively** with the directory mode defaulting to `0755` (modulo the process umask). A missing parent does not produce an error.

### Read-before-overwrite

When the target path **exists**, the tool overwrites it **only if the caller has read it earlier in the same session via [`Read`](./read.md)**. If the caller has not read the file, the write is refused with the error string given under [Errors](#errors-the-server-returns) (`File has not been read yet. Read it first before writing to it.`).

This is enforced by per-session state; see [Session state model](#session-state-model) below for the read-tracking model used by `Write` and the other state-aware tools in this server.

### Modified-since-read

When the target path **exists and has been read**, the tool further checks that the file has not been modified on disk since the read. The check uses the file's modification time at integer-millisecond precision (`Math.floor(mtimeMs)`):

- If the current `mtime` equals the stored `mtime`, the file is considered unmodified and the write proceeds.
- If the current `mtime` is **strictly greater than** the stored `mtime`, the file is considered potentially modified and a content-equality fallback applies (see below).

#### Content-equality fallback (full read only)

If the original read was a **full read** (no `offset`, no `limit`), the server compares the content that was recorded at read time against the current on-disk content. The comparison is performed against the LF-normalised content (`\r\n` → `\n`, decoded as UTF-8) and uses a SHA-1 digest, base64url-encoded without padding, that is computed once at read time and stored alongside the cached content.

- If the digests (or the contents directly when the digest is absent) match, the on-disk change is treated as noise (for example, a formatter that produced the same bytes), and the write proceeds.
- If they do not match, the write is refused with the error string for the modified-since-read condition.

If the original read was a **partial read** (any `offset` or `limit` set), the content-equality fallback is **skipped** and the write is refused unconditionally when `mtime` advanced. The caller is expected to re-read the file before retrying.

### Two-tier check (pre-flight + in-call TOCTOU)

The server performs the read-before-overwrite and modified-since-read checks **twice**:

1. **Pre-flight** (before any write begins): the server checks both conditions and refuses early if they fail.
2. **In-call TOCTOU re-check** (immediately before the write): the server re-checks both conditions to close the time-of-check / time-of-use race window between the pre-flight check and the actual write.

The two checks use the same logic but emit **different error wording** for the modified-since-read case (see [Errors](#errors-the-server-returns)). For the read-before-overwrite case the wording is identical between the two checks.

### Per-path mutex

Multiple concurrent writes that target the same `file_path` within a single session are serialised through a per-path mutex. The mutex covers the in-call TOCTOU re-check and the write itself, so two in-flight writes cannot race between the re-check and the rename.

### Symlink safety

If `file_path` is a symbolic link, the write is refused with the error string for the symlink-write condition. The caller is expected to resolve the symlink and pass the real target path explicitly.

The symlink check is enforced both at permission-check time and at write time; the server compares the resolved targets at the two moments and refuses if they differ, to detect a symlink swap during the call.

### Atomicity

The write is performed atomically by default:

1. Write the new content to a temporary file at `<file_path>.tmp.<pid>.<timestamp>` in the same directory.
2. `fsync` the temporary file.
3. `rename(2)` the temporary file to `file_path`.

If any step in the atomic path fails (for example, the filesystem rejects same-directory `rename`, or there is no permission to create the temporary file), the server falls back to a non-atomic direct write. The fallback is logged via the operational logger but does not change the response shape.

### Post-write verification

After a successful write, the server `stat`s the new file and verifies that the on-disk size equals the byte length of `content`. If the sizes differ (for example, a network-mounted filesystem truncated the write), the operation is treated as a failure and surfaced as an I/O error.

### State refresh after successful write

After a successful write, the per-session read-tracking entry for `file_path` is refreshed: the recorded content is set to the new content, the `mtime` is read from the just-written file, the content-hash is recomputed, and `offset`/`limit` are cleared. The new write counts as a full read for the purposes of any subsequent overwrite.

### Empty `content`

Empty `content` is legal and produces an empty file. The write still goes through the read-before-overwrite and atomicity logic.

### Trailing newline

The tool writes `content` exactly as provided. It does not add or strip a trailing newline.

### File mode

Newly created files use mode `0644` (rw-r--r--) modulo the process umask. Existing files keep their mode across overwrites.

## Session state model

The read-tracking state used by Read-before-overwrite and Modified-since-read is **per-MCP-session**, keyed by the `Mcp-Session-Id` header from the Streamable HTTP transport.

### Storage

The state is an **LRU cache** with a default maximum of 256 entries per session. Each entry is keyed by the normalised absolute file path and carries `{content, contentHash, mtime, offset, limit, isPartialView}`. When the LRU exceeds its maximum, the least-recently-used entry is evicted. Evicted files appear to the server as unread on the next Read-before-overwrite check, and the caller is expected to re-read them.

The maximum is tunable per-deployment via the `CCFS_MCP_READ_TRACKING_MAX_ENTRIES` environment variable.

### Entries seeded by Read

The `Read` tool seeds the per-session state on every successful read: full reads, partial reads (`offset` / `limit`), notebook reads, and image reads. A bash tool, when present, will seed the state when it recognises a single-file read-equivalent shell invocation; see [`bash.md`](./bash.md) for the recognised list. Until the bash tool is implemented, only an explicit `Read` call seeds the state.

### Session lifecycle

A session begins with the MCP `initialize` request and ends with one of:

- A client `DELETE` request against the session id (graceful termination).
- A transport-disconnect detection by the underlying HTTP server (ungraceful termination).

When a session ends, the server keeps the read-tracking entries for a **TTL grace period** (default 60 seconds). If the same session id is presented again within the grace window, the state is reattached and the read-tracking entries are reused. If the grace window elapses, the entries are cleaned up.

The grace duration is tunable via the `CCFS_MCP_SESSION_TTL_SECONDS` environment variable.

## Edge cases and constraints

- An empty `file_path` is an error before any I/O happens.
- A `file_path` that is not absolute is an error before any I/O happens.
- A `file_path` that resolves to a directory is surfaced as the underlying filesystem `EISDIR`; see [Errors](#errors-the-server-returns).
- A `file_path` whose normalised absolute form differs only by trailing slashes, `/./`, or symlink resolution maps to the same state entry as the canonical form.
- Re-writing a file that has not changed since the read succeeds even when `content` is identical to the on-disk content.

## Errors the server returns

The errors below are surfaced as MCP tool errors. Each message is wrapped in `<tool_use_error>...</tool_use_error>`. The pre-flight wrapper uses the message verbatim; the in-call wrapper prefixes with `Error calling tool (Write): ` to indicate that the failure was caught after the write attempt started.

### Read-before-overwrite (existing-but-unread)

The target exists on disk but the caller has not read it in this session (or the cached entry has `isPartialView: true`).

```
File has not been read yet. Read it first before writing to it.
```

The wording is identical in both pre-flight and in-call paths.

### Modified-since-read

The target exists, has been read, but the file's `mtime` advanced and the content-equality fallback either did not match (full read) or was skipped (partial read).

**Pre-flight wording**:

```
File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.
```

**In-call TOCTOU re-check wording** (intentionally different to indicate the race was caught later in the lifecycle):

```
File content has changed since it was last read. This commonly happens when a linter or formatter run via Bash rewrites the file. Call Read on this file to refresh, then retry the edit.
```

### Symlink write refused

The target `file_path` is a symbolic link, or the resolved target at write time differs from the resolved target at permission time (a symlink was swapped during the call).

```
Refusing to write through symlink: <file_path>. Resolve the symlink and pass the real target path explicitly.
```

### Path is a directory / permission denied / I/O failure

These are surfaced as the underlying filesystem error, wrapped by the in-call prefix:

```
EISDIR: illegal operation on a directory, read '<file_path>'
EACCES: permission denied, open '<file_path>'
EIO: i/o error, write '<file_path>'
```

The exact wording follows the host runtime's filesystem error formatter (Node.js `libuv` format on the upstream tool).

### Relative / empty path (schema validation)

These are caught by schema validation before any handler runs:

```
file_path is required
file_path must be an absolute path, not relative: <file_path>
```

## Permissions and security

- The upstream `Write` tool requires a permission prompt by default. Allow/deny rules use the `Edit(<path-pattern>)` form, which covers `Write`, `Edit`, and `NotebookEdit` together.
- The upstream tool maintains a list of **protected paths** that are not auto-approved even under permissive permission modes. These include version-control internals (`.git`, `.config/git`), editor-specific directories, shell startup files, and certain configuration files in the project root.
- This MCP server is **sandbox-agnostic**: it does not maintain a protected-path list, does not apply allow/deny rules, and does not require a permission prompt. The expectation is that the hosting environment exposes only the directories the agent is meant to be able to write to.
- See [Common conventions](./README.md#common-conventions) for the cross-cutting response-shape rule that applies to every tool in this server.

## Implementation status

🟢 **Drafted.** The spec is essentially complete; remaining open points (see [Known gaps](#known-gaps)) will be resolved through implementation observation. The implementation itself has not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Read-equivalent shell commands.** The upstream tool counts certain single-file shell invocations as Read-equivalents for satisfying the read-before-overwrite precondition. The recognised commands include `cat` (with optional `-n`/`--number`), `nl`, `bat`, `batcat`, `sed -n 'Np'` / `sed -n 'N,Mp'`, `head`, `tail`, `grep`/`egrep`/`fgrep` (single command, requires exit zero), and `rg` (single command, requires exit zero). None can be combined with pipes or shell redirects. The MCP server will only honour these once it also implements the bash tool; the rules will then live in [`bash.md`](./bash.md). Until then, only an explicit `Read` call seeds the read-tracking state.
- **`isPartialView` trigger conditions.** The upstream tool sets an `isPartialView` flag on certain read-state entries (text Read partial, transcript restore, nested memory). The exact set of conditions under which a plain partial Read produces a partial-view entry has not been fully pinned down. The server's initial implementation treats every `Read` (full or partial) as producing a non-partial-view entry; refinement is deferred until a case is observed where the upstream tool diverges from this behaviour.
- **TTL grace default value.** The default 60-second grace period is a working choice: long enough to ride out transient network drops, short enough to bound the memory cost of orphaned sessions. The value is tunable via the environment variable above.
- **LRU max entries default value.** The default 256-entry cap is a working choice. The value is tunable via the environment variable above.

## Known limitations

The following behaviours of the upstream Claude Code CLI's built-in `Write` cannot be reproduced by an MCP server interposed between the CLI and the filesystem, and are recorded here so callers know which built-in behaviours are not available through this server.

- **Read-before-overwrite tracking across resumed conversations.** The upstream Claude Code CLI does not persist its MCP session id across CLI process lifetimes. Resuming a conversation starts a new CLI process which initialises a fresh MCP session with a new session id. This server's read-tracking state is keyed by MCP session id, so previously-read files appear unread to the server after a resume, and a subsequent write is refused until the file is read again in the new session.

- **No graceful session termination from the upstream CLI.** The upstream Claude Code CLI does not send the MCP `DELETE` request when it exits. The server therefore relies on transport-disconnect detection to clean up per-session state. Servers running behind load balancers or proxies that hold the underlying connection open after the client is gone may observe state lingering until their own idle timeouts fire.

- **PostToolUse hook re-sync.** The upstream CLI runs PostToolUse hooks (for example, a formatter) after every successful write, then re-reads the file if the hook bumped the `mtime` and seeds its read-tracking state with the post-hook content. This is an in-CLI feature with no MCP-level surface. Callers running formatters or other automatic rewriters as PostToolUse hooks need to issue an explicit `Read` after such hooks fire, before the next `Write` against the same path.

- **Background-session worktree isolation.** The upstream CLI maintains an isolation invariant that prevents a background session from writing to files that belong to a different worktree, and emits a tool error when a write would cross the boundary. The MCP server has no concept of multiple worktrees within a session and does not enforce this isolation.

- **Personal-/team-memory secret scanning.** The upstream CLI runs a secret-scanning step when writing into directories the CLI treats as "memory" (personal-memory or team-memory paths). The MCP server has no notion of these CLI-internal directories and does not run the equivalent scan. Callers who need to enforce secret-scanning on writes should layer it externally.

- **Experiment-flag escape hatches.** The upstream CLI exposes experiment flags that, when enabled, bypass the read-before-overwrite and modified-since-read guards. The MCP server intentionally does not implement these escape hatches: the guards exist to protect agents from clobbering concurrent edits, and an MCP-level bypass would undermine that contract for every consumer of the server.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (Write tool behavior section), `permissions` (Read and Edit section), `permission-modes` (Protected paths section), `security` (Write access restriction section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) (`tool-description-write.md`, `tool-description-write-read-existing-file-first.md`): the upstream prompt-level descriptions sent to the model, including the wording the CLI advertises for the read-before-overwrite precondition.
- [`1rgs/nanocode`](https://github.com/1rgs/nanocode): the `write` tool implementation in an independent minimal Python reference, useful as a cross-check for the existing-but-unread refusal and the empty-content case.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
