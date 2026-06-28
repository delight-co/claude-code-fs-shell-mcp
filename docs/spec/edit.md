# Edit

## Purpose

Make a targeted change to an existing file by replacing one exact substring with another. `Edit` is not regex, not fuzzy, and not patch-based: the caller supplies the precise pre-image and the precise post-image, and the tool swaps the first for the second. Where [`Write`](./write.md) is whole-file, `Edit` is surgical.

## Signature

```json
{
  "name": "edit",
  "input_schema": {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "Absolute path to the file to edit. The file must already exist and have been read in this session."
      },
      "old_string": {
        "type": "string",
        "description": "Exact text to replace. Whitespace and indentation must match the file byte-for-byte."
      },
      "new_string": {
        "type": "string",
        "description": "Text that replaces `old_string`."
      },
      "replace_all": {
        "type": "boolean",
        "description": "When `true`, replaces every occurrence of `old_string`. When omitted or `false`, the tool requires `old_string` to appear exactly once.",
        "default": false
      }
    },
    "required": ["file_path", "old_string", "new_string"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names and the `replace_all` default mirror what the model sees in the prompt-level descriptions and are stable across the captured CLI versions.

## Semantics

The tool runs an ordered chain of checks. Each one must pass before the next is evaluated; the first failure short-circuits with the matching error.

### Pre-flight chain

1. **Path normalisation.** `file_path` must be a non-empty absolute path. The path is resolved (NFC normalisation is *not* applied; the upstream tool does not Unicode-normalise either, despite an earlier draft of this spec saying so).
2. **No-op rejection.** If `old_string === new_string`, the call is refused immediately.
3. **File size cap.** Files larger than **1 GB** (`1,073,741,824` bytes) cannot be edited.
4. **Read-before-edit.** If the target exists on disk, the caller must have read it earlier in the same MCP session. The read-tracking state is shared with [`Write`](./write.md#session-state-model).
5. **Modified-since-read.** If the target exists and has been read, mtime is checked against the stored timestamp; on advance, a full-read entry triggers the content-equality fallback (see [Write spec § Modified-since-read](./write.md#modified-since-read)). The same logic applies; a partial-read entry with an mtime advance is refused unconditionally.
6. **`.ipynb` rejection.** `.ipynb` paths are refused with a hint to use `NotebookEdit`.
7. **Match.** `old_string` must appear in the file as supplied, or as a Unicode-normalised equivalent (see [String matching](#string-matching)).
8. **Uniqueness.** With `replace_all` left at its default, the (normalised) match count must be exactly one. With `replace_all: true`, more than one match is fine.

### In-call TOCTOU re-check

Immediately before the write, under the per-path mutex, the read-before-edit and modified-since-read checks are repeated. The pre-flight wording and the in-call wording for the modified-since-read failure differ; see [Errors the server returns](#errors-the-server-returns).

### String matching

`old_string` is searched against the file in a small chain of strategies, returning the first hit:

1. **Exact substring.** `file.includes(old_string)`.
2. **Smart-quote normalised.** Both the file and `old_string` are normalised so that the curly quotes `U+2018`, `U+2019`, `U+201C`, `U+201D` map to the straight forms `'` and `"`, then searched again. If a normalised match is found, the actual substring from the file (preserving its smart-quote characters) is what gets replaced.
3. **Unicode-escape literal → real character.** If `old_string` contains `\uXXXX` escape sequences, each is converted to the real character (preserving `\\\\u` from accidental conversion) and the file is searched again with the converted form.
4. **Real character → Unicode-escape regex.** If `old_string` contains non-ASCII characters, those characters are converted to a regex that matches both uppercase and lowercase `\uXXXX` representations, then the file is searched with that regex.

The replacement uses the actual substring that hit (so the file's existing form is preserved). The `new_string` is also adjusted before substitution:

- If the matched form differs from `old_string` by smart-quote normalisation, the same smart-quote synchronisation is applied to `new_string`, switching its straight quotes to the file's smart quotes (the choice of left/right curly depends on the surrounding character).
- If the matched form differs by Unicode-escape conversion (strategy 3 or 4), `new_string`'s representation of non-ASCII characters is converted to match (e.g. if the file represents `é` as the literal `é`, `new_string`'s real `é` is converted to `é`; case follows whichever case dominated `old_string`).

### `replace_all` semantics

The substitution is performed against the **original file content**, in a single pass:

- `replace_all: false`: the first occurrence (after the normalisation chain above) is replaced.
- `replace_all: true`: every non-overlapping occurrence is replaced, using a callback-form `String.prototype.replaceAll` so that `$`-sequences in `new_string` (`$&`, `$1`, `$$`, …) are treated as literal characters, not substitution patterns. Because the substitution is against the original content (not the running result), inserting an `old_string` that contains itself does not cause an infinite loop or chained replacement.

### Deletion (empty `new_string`) and trailing-newline absorption

An empty `new_string` deletes the matched region. When the matched `old_string` does not end with `\n` but `old_string + "\n"` is present in the file, the tool deletes `old_string + "\n"` instead of just `old_string`, so an unwanted blank line is not left behind.

### Per-path mutex, symlink safety, atomicity, post-write verification, state refresh

These mechanisms are identical to those documented in [`Write`](./write.md):

- **Per-path mutex** serialises edits and writes targeting the same path across sessions.
- **Symlink safety** refuses paths that resolve to a symbolic link, with the permission-time / write-time resolved-target diff check.
- **Atomicity** uses temp-file + rename, with a non-atomic fallback.
- **Post-write verification** confirms the on-disk byte size matches the rendered content.
- **State refresh** updates the read-tracking entry with the new content, mtime, and hash, with `offset`/`limit` cleared.

In addition, after a successful edit the server emits an LSP `changeFile` + `saveFile` notification when an LSP server is configured, matching the upstream behaviour. LSP failures are logged but do not change the response shape. (No LSP integration ships in the initial server; see [Known gaps](#known-gaps).)

### Encoding handling

- Files are decoded as UTF-8 by default, or UTF-16LE when the first two bytes are `FF FE` (BOM).
- Internally CRLF is normalised to LF for matching and hashing. When the file's detected line endings are CRLF, the rendered content is rewritten with CRLF on the way out.
- Byte-order marks, encoding changes, and other transformations are not introduced by the tool.

## Edge cases and constraints

- A file that does not exist on disk and a non-empty `old_string` is rejected (read-before-edit fails — there is nothing to have read).
- A file that does not exist and an **empty** `old_string` is treated as a creation request: the file is created with `new_string` as its content.
- A file that exists, is non-empty, and an **empty** `old_string` is rejected (cannot create a new file because one already exists).
- A `.ipynb` path is rejected regardless of the other inputs.
- `replace_all: true` against zero matches still fails the match check.
- `old_string === new_string` is rejected as a no-op.
- `old_string` whose normalised match count exceeds one with `replace_all` omitted or `false` is rejected with the count returned in the error.

## Errors the server returns

The errors below are surfaced as MCP tool errors. Each message is wrapped in `<tool_use_error>...</tool_use_error>`. The pre-flight wrapper uses the message verbatim; the in-call wrapper prefixes with `Error calling tool (Edit): `.

### (a) No-op

```
No changes to make: old_string and new_string are exactly the same.
```

### (b) Permission deny rule

```
File is in a directory that is denied by your permission settings.
```

### (c) File too large

```
File is too large to edit (<size>). Maximum editable file size is 1GB.
```

Where `<size>` is the file size formatted with binary units (`KB`/`MB`/`GB`).

### (d) File does not exist

```
File does not exist. Note: your current working directory is <cwd>.
```

When a likely candidate file exists nearby (same-stem under cwd, or a single sibling that differs only by case), a suffix is appended:

```
 Did you mean <suggested-path>?
```

### (e) Create-mode against an existing non-empty file

```
Cannot create new file - file already exists.
```

### (f) `.ipynb` rejection

```
File is a Jupyter Notebook. Use the NotebookEdit to edit this file.
```

### (g) Read-before-edit (existing-but-unread)

The same wording is used by pre-flight and in-call paths:

```
File has not been read yet. Read it first before writing to it.
```

### (h) Modified-since-read

**Pre-flight wording**:

```
File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.
```

**In-call TOCTOU re-check wording** (different to indicate the race was caught later in the lifecycle):

```
File content has changed since it was last read. This commonly happens when a linter or formatter run via Bash rewrites the file. Call Read on this file to refresh, then retry the edit.
```

### (i) String not found

```
String to replace not found in file.
String: <old_string>
```

When `old_string` contains a `\uXXXX` escape sequence or a non-ASCII character, an additional note is appended on a new line:

```

(note: Edit also tried swapping \uXXXX escapes and their characters; neither form matched, so the mismatch is likely elsewhere in old_string. Re-read the file and copy the exact surrounding text.)
```

### (j) Multiple matches without `replace_all`

```
Found <count> matches of the string to replace, but replace_all is false. To replace all occurrences, set replace_all to true. To replace only one occurrence, please provide more context to uniquely identify the instance.
String: <old_string>
```

### (k) Symlink write refused

```
Refusing to write through symlink: <file_path>. Resolve the symlink and pass the real target path explicitly.
```

A second symlink-related error fires when the parent-directory resolution at write time differs from the resolution captured at permission-check time (a symlink swap during the call):

```
Refusing to write <file_path>: its parent-directory symlink resolution changed after permission was checked.
```

### (l) Path is a directory / permission denied / I/O failure

Surfaced as the underlying filesystem error, wrapped by the in-call prefix:

```
EISDIR: illegal operation on a directory, read '<file_path>'
EACCES: permission denied, open '<file_path>'
EIO: i/o error, write '<file_path>'
```

### (m) Post-write size mismatch

```
Write verification failed: <file_path> is <actual-bytes> bytes on disk, expected <expected-bytes>. The filesystem may have silently truncated the write (network drive / cloud sync).
```

### (n) Relative / empty path

Caught by schema validation:

```
file_path is required
file_path must be an absolute path, not relative: <file_path>
```

## Success message

After a successful edit, the LLM-facing result is one of the variants below. The pattern is `The file <file_path> has been updated[<modifier>][<replace-all-note>][<suffix>].` where each bracketed segment is included only when applicable.

| condition | content |
|---|---|
| Default (single replacement) | `The file <file_path> has been updated successfully. (file state is current in your context — no need to Read it back)` |
| Single replacement, user-modified | `The file <file_path> has been updated successfully.  The user modified your proposed changes before accepting them. .` |
| `replace_all: true` | `The file <file_path> has been updated. All occurrences were successfully replaced. (file state is current in your context — no need to Read it back)` |
| `replace_all: true`, user-modified | `The file <file_path> has been updated.  The user modified your proposed changes before accepting them. . All occurrences were successfully replaced.` |
| Single replacement after stale recovery (see [Known limitations](#known-limitations)) | `The file <file_path> has been updated successfully. (note: the file had been modified on disk since you last read it — the edit applied cleanly, but the file contains other changes not in your context. Read it before edits that depend on surrounding content.)` |
| `replace_all: true` after stale recovery | `The file <file_path> has been updated. All occurrences were successfully replaced. (note: the file had been modified on disk since you last read it — the edit applied cleanly, but the file contains other changes not in your context. Read it before edits that depend on surrounding content.)` |

Notes:

- The `user-modified` segment begins with a literal period and is followed by two spaces and a trailing space, which produces a `..` adjacency with the surrounding sentence's terminator. This is reproduced verbatim for parity.
- The "(file state is current …)" suffix is omitted when the user-modified segment is present.
- The `staleRecovered` branch is gated on the upstream experiment flag the server does not implement (see [Known limitations](#known-limitations)); in this server, the stale-recovery variants of the success message do not fire.

## Permissions and security

- The upstream `Edit` tool requires a permission prompt by default. Allow/deny rules use `Edit(<path-pattern>)` and cover `Write`, `Edit`, and `NotebookEdit` together. Granting an `Edit(...)` rule also grants read access to the same path.
- The upstream protected-paths list applies to `Edit` exactly as it applies to `Write` — see [`write.md`](./write.md#permissions-and-security).
- This MCP server is **sandbox-agnostic**: no protected-paths list, no allow/deny rules, no permission prompt. The hosting environment is expected to constrain which paths the agent can touch.
- See [Common conventions](./README.md#common-conventions) for the cross-cutting response-shape rule.

## Implementation status

🟢 **Drafted.** The spec is essentially complete; remaining open points (see [Known gaps](#known-gaps)) will be resolved through implementation observation. The implementation itself has not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Read-equivalent shell commands.** The upstream tool counts certain single-file shell invocations as Read-equivalents for satisfying the read-before-edit precondition. The recognised set is the same as for [`Write`](./write.md#known-gaps) (`cat`, `nl`, `bat`, `batcat`, `sed -n 'Np'` / `sed -n 'N,Mp'`, `head`, `tail`, `grep` / `egrep` / `fgrep`, `rg`, with no pipes or redirects; `grep` / `rg` additionally require an exit-zero result). The full rules are now pinned in [`bash.md`'s Read-equivalent shell commands section](./bash.md#read-equivalent-shell-commands); the seeding will only take effect once the bash tool implementation lands. Until then, only an explicit `Read` call seeds the read-tracking state.
- **Smart-quote normalisation and unicode-regex match strategies (initial-implementation gap).** The initial implementation honours only strategy 1 (exact substring) and strategy 3 (`\uXXXX` literal → real character) of the four-strategy match chain. Strategy 2 (smart-quote normalisation) and strategy 4 (real character → case-insensitive `\uXXXX` regex), and the corresponding smart-quote / unicode synchronisation of `new_string`, are deferred to follow-up work; callers must pass `old_string` in the same form (smart vs straight quotes, escape vs real characters) as the file content for those less-common cases. The dominant smart-quote code-point pairs (`U+2018`/`U+2019` for single, `U+201C`/`U+201D` for double) are documented here so the eventual implementation matches the upstream tool.
- **`isPartialView` trigger conditions.** Inherited from the [Write spec's known gaps](./write.md#known-gaps); the server's initial implementation treats every `Read` as producing a non-partial-view entry.
- **Settings-file schema validation.** The upstream tool runs a schema validation step when the target path is its own settings file (`~/.claude/settings.json` and `~/.claude/settings.local.json`). The MCP server does not own that schema and skips this step; an edit to those files is treated like any other file.
- **Memory-file frontmatter injection.** The upstream tool injects YAML frontmatter into Markdown files under its team-/personal-memory directories on first write. The MCP server has no concept of those directories and does not perform the injection. See also the related Known limitation below.
- **LSP integration.** The upstream tool sends `changeFile` + `saveFile` notifications to the configured LSP server when it edits a file open in the IDE. The MCP server does not currently integrate with an LSP and skips this step.

## Known limitations

These behaviours of the upstream Claude Code CLI's built-in `Edit` cannot be reproduced by an MCP server interposed between the CLI and the filesystem. The first four are shared with the Write spec; the remaining items are Edit-specific.

- **Read-before-edit tracking across resumed conversations.** The upstream Claude Code CLI does not persist its MCP session id across CLI process lifetimes. Resuming a conversation starts a new CLI process with a fresh session id, so previously-read files appear unread to the server after a resume.

- **No graceful session termination from the upstream CLI.** The upstream CLI does not send the MCP `DELETE` request when it exits. The server relies on transport-disconnect detection to clean up per-session state.

- **PostToolUse hook re-sync.** The upstream CLI runs PostToolUse hooks after every successful edit and re-reads the file when a hook bumps mtime, seeding its own read-tracking state with the post-hook content. The MCP server has no in-CLI hook surface and cannot drive this.

- **Background-session worktree isolation.** The upstream CLI enforces a worktree-isolation invariant on background sessions, refusing edits that would write to files outside the session's worktree. The MCP server has no notion of background sessions or worktrees and does not enforce this.

- **`tengu_velvet_hammer` escape hatch.** The upstream CLI exposes an experiment flag that bypasses the read-before-edit guard. The MCP server intentionally does not implement it: the guard protects agents from clobbering concurrent edits, and an MCP-level bypass would undermine that contract.

- **`tengu_cedar_sundial` stale-recovery.** The upstream CLI exposes an experiment flag that, when enabled, allows an edit to proceed even after `mtime` has advanced and the content hash mismatches, provided `old_string` still matches unambiguously in the new content. When this fires, the CLI's success message gains an explanatory `(note: …)` suffix. The MCP server intentionally does not implement this: silently editing a file whose context has changed under the agent's feet defeats the purpose of the modified-since-read check. The corresponding success-message variants are documented above for parity, but the server never emits them in practice.

- **Personal- and team-memory secret scanning.** The upstream CLI runs a secret scanner against `new_string` when the target is in one of its memory directories. The MCP server has no notion of these directories and does not scan.

- **Memory-file frontmatter injection.** See [Known gaps](#known-gaps); the upstream tool injects YAML frontmatter into Markdown files under its memory directories on first write. Not reproduced here.

- **LSP `changeFile` / `saveFile` notification.** See [Known gaps](#known-gaps); the upstream tool notifies an attached LSP server after each edit. Not currently reproduced.

- **Settings-file schema validation.** See [Known gaps](#known-gaps); the upstream tool runs a JSON-schema check when editing its own settings file. The MCP server treats those paths like any other.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (Edit tool behavior section), `permissions` (Read and Edit section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) (`tool-description-edit.md`, `tool-description-edit-single-replacement.md`, `tool-description-edit-minimal-old_string-guidance.md`): the upstream prompt-level descriptions sent to the model.
- [`1rgs/nanocode`](https://github.com/1rgs/nanocode): the `edit` tool implementation in an independent minimal Python reference, useful as a cross-check for the three-checks ordering and the `replace_all` semantics.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
