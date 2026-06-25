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
        "description": "Absolute path to the file to write. Parent directories must exist."
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

- `file_path` must be an absolute path. Relative paths are rejected without writing anything.
- When the target path does **not** exist, the tool creates it. The new file is opened, the provided `content` is written verbatim, and the tool reports success.
- When the target path **exists**, the tool overwrites it — **but only if the caller has read it first in the current session**. If the file exists on disk but the caller has not gone through [`Read`](./read.md) for it earlier in the conversation, the write is refused with an error and the caller is directed to read first.
- The upstream contract also accepts certain shell-based reads as equivalent to `Read` for satisfying this pre-condition (single-file `cat`, `head`, `tail`, `sed -n 'X,Yp'`, `grep`, `egrep`, `fgrep`, without pipes or redirects). Whether this server tracks shell reads in the same way depends on whether it also handles the shell tool; see [Known gaps](#known-gaps).
- The write replaces the entire file. There is no append mode. To change part of an existing file, the upstream guidance is to use [`Edit`](./edit.md) instead.
- Modified-since-read check: if the file has been modified on disk after the caller's `Read`, the write is refused with an error and the caller is directed to read again. The check is observable in the upstream tool; the exact mechanism (mtime, hash, or both) is not published.

## Edge cases and constraints

- Existing-but-unread files: refused, even if the new `content` is identical to the on-disk content.
- Empty `content` is legal and produces an empty file.
- Trailing newline: the tool writes `content` exactly as provided. It does not add or strip a trailing newline.
- Parent directory: the upstream tool requires the parent directory to exist. Whether this server creates missing parent directories silently is an implementation choice; see [Known gaps](#known-gaps).
- File permissions: the upstream tool does not publish the mode of newly created files. The implementation will pick a standard, conservative default (for example, `0644` on POSIX systems, with `0755` for parent directories if it creates them).
- Atomicity: the upstream tool does not document write-then-rename atomicity. The implementation will document whatever guarantee it provides in its release notes.

## Error behaviour

Concrete error string formats are **TBD**. The implementation will produce errors that distinguish:

- existing-but-unread file (refused by the read-before-overwrite check),
- modified-since-read file (refused by the freshness check),
- target-is-a-directory,
- permission failures,
- I/O failures.

Every error includes the requested path so the caller can correct it.

## Permissions and security

- The upstream `Write` tool requires a permission prompt by default. Allow/deny rules use the `Edit(<path-pattern>)` form, which covers `Write`, `Edit`, and `NotebookEdit` together. Allowing an `Edit(<pattern>)` also grants read access to the same path.
- The upstream tool maintains a list of **protected paths** that are not auto-approved even under the more permissive permission modes. These include version-control internals (`.git`, `.config/git`), editor-specific directories, shell startup files, and certain configuration files in the project root. The protected-path list is policy that lives in the CLI itself.
- This MCP server is **sandbox-agnostic**. It does not maintain a protected-path list, does not apply allow/deny rules, and does not require a permission prompt. The expectation is that the hosting environment exposes only the directories the agent is meant to be able to write to.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Shell reads counting as `Read`.** The upstream contract recognises certain shell commands as equivalent to `Read` for satisfying the read-before-overwrite check. This server will only honour this if and when it also handles the shell tool, and the rules will then live in [`bash.md`](./bash.md). Until then, only an explicit `Read` call satisfies the precondition.
- **Parent-directory creation.** The upstream tool requires the parent directory to exist. The implementation will pick one of the following behaviours and record the choice here: (a) refuse the write, (b) create missing parents with a default mode and report it in the result, or (c) make the behaviour selectable.
- **Atomicity guarantees.** The upstream tool does not publish whether writes are atomic. The implementation will document its own guarantee (atomic via temp-file + rename, or non-atomic with a clear note).

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Write tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Read and Edit section), [`permission-modes`](https://code.claude.com/docs/en/permission-modes) (Protected paths section), [`security`](https://code.claude.com/docs/en/security) (Write access restriction section).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-write.md`, `tool-description-write-read-existing-file-first.md`.
- `1rgs/nanocode`: the `write` tool implementation, used as an independent cross-check for the existing-but-unread refusal and the empty-content case.
