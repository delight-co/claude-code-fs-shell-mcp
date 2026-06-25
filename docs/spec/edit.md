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

`Edit` runs three checks in order. The order matters: each check must pass before the next is evaluated.

1. **Read-before-edit.** The caller must have read the file in the current session, and the file must not have changed on disk since that read. The session-level read tracking is the same that [`Write`](./write.md) uses.
2. **Match.** `old_string` must appear in the file exactly as supplied — byte for byte, with whitespace and indentation intact. A single character of difference is enough to miss.
3. **Uniqueness.** With `replace_all` left at its default, `old_string` must appear exactly once. If it appears more than once, the caller is expected to either lengthen `old_string` with surrounding context to pin one occurrence, or set `replace_all` to `true` to apply the change everywhere.

When all three checks pass, the tool writes the file back with the substitution applied and reports success. The success report shows the area around the change so the caller can confirm the right region was touched.

`new_string` may be empty. An empty `new_string` deletes the matched region.

The upstream contract recognises certain shell commands as equivalent to `Read` for satisfying the read-before-edit check (single-file `cat`, `head`, `tail`, `sed -n 'X,Yp'`, `grep`, `egrep`, `fgrep`, without pipes or redirects). Whether this server honours that recognition depends on whether it also handles the shell tool; see [Known gaps](#known-gaps).

## Edge cases and constraints

- A file that does not exist on disk fails the read-before-edit check (there is nothing to have read).
- A file that was read earlier but has since been modified on disk fails check 1 and the caller is asked to re-read.
- A `old_string` that does not appear in the file fails check 2.
- A `old_string` that appears more than once with `replace_all` omitted or `false` fails check 3.
- An `old_string` equal to `new_string` is a no-op; the implementation may either accept it as a successful zero-change edit or report an error. The upstream documentation does not pin the behaviour.
- `replace_all: true` replaces every occurrence in a single pass. The implementation must not allow a `new_string` that contains `old_string` to cause an infinite loop or unintended chained replacement; the substitution is over the original file content, not the running result.
- The substitution is byte-exact. Encoding-changing transformations (CRLF ↔ LF, BOM insertion or removal, encoding normalisation) are not performed.

## Error behaviour

Concrete error string formats are **TBD**. The implementation will produce errors that distinguish:

- not-read-this-session (check 1 fails because the caller never read the file),
- modified-since-read (check 1 fails because the file changed on disk),
- not-found (check 2 fails because `old_string` is not in the file),
- not-unique (check 3 fails and `replace_all` is not set),
- no-op (`old_string == new_string`, if the implementation chooses to reject it),
- permission failures and I/O failures.

Every error includes enough context for the caller to fix the input: the requested path, and for not-unique errors, the count of matches.

## Permissions and security

- The upstream `Edit` tool requires a permission prompt by default. Allow/deny rules use `Edit(<path-pattern>)` and cover `Write`, `Edit`, and `NotebookEdit` together. Granting an `Edit(...)` rule also grants read access to the same path.
- The upstream protected-paths list applies to `Edit` exactly as it applies to `Write` — see [`write.md`](./write.md#permissions-and-security).
- This MCP server is **sandbox-agnostic**. It does not maintain a protected-paths list and does not apply allow/deny rules. The hosting environment is expected to constrain which paths the agent can touch.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Shell reads counting as `Read`.** The upstream contract recognises certain shell commands as equivalent to `Read` for satisfying check 1. This server will only honour that if and when it also handles the shell tool, and the rules will live in [`bash.md`](./bash.md).
- **No-op handling.** Whether `old_string == new_string` is treated as a successful zero-change edit or as an error is a deliberate implementation choice that will be recorded here at implementation time.
- **Modified-since-read detection mechanism.** The upstream tool does not publish whether the freshness check uses mtime, content hashing, or both. The implementation will pick a mechanism and document it.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Edit tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Read and Edit section).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-edit.md`, `tool-description-edit-single-replacement.md`, `tool-description-edit-minimal-old_string-guidance.md`.
- `1rgs/nanocode`: the `edit` tool implementation, used as an independent cross-check for the three-checks ordering and the `replace_all` semantics.
