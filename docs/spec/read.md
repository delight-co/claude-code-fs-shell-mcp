# Read

## Purpose

Read the contents of a file from the local filesystem and return them in a form a coding agent can reason about. For plain text, that means the file contents with line numbers prepended. For paginated formats (PDF), it means an explicitly windowed slice. For binary formats the upstream tool understands (images, Jupyter notebooks), it means a structured representation rather than raw bytes.

The tool is the canonical entry point for an agent to ground itself in a file before editing it. Its read-before-edit contract underpins both [`Write`](./write.md) (overwrite) and [`Edit`](./edit.md) (in-place change).

## Signature

```json
{
  "name": "read",
  "input_schema": {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "Absolute path to the file to read."
      },
      "offset": {
        "type": "integer",
        "description": "1-based line number to start reading from. Use together with `limit` when paginating through a large file.",
        "minimum": 1
      },
      "limit": {
        "type": "integer",
        "description": "Maximum number of lines to return.",
        "minimum": 1
      },
      "pages": {
        "type": "string",
        "description": "PDF-only page range, for example `\"1-5\"` or `\"3\"`. Up to 20 pages per call."
      }
    },
    "required": ["file_path"]
  }
}
```

The exact `offset` indexing base, and which parameter combinations are legal for which file types, are reconstructed from the prompt-level descriptions and the independent reference; the upstream tool does not publish a JSON schema.

## Semantics

- `file_path` must be an absolute path. Relative paths are rejected without reading the file.
- The default response prepends line numbers in `cat -n`-like form so the model can refer to specific lines unambiguously.
- When neither `offset` nor `limit` is provided and the file is small enough to fit, the entire file is returned. Otherwise a default-sized prefix is returned together with a notice telling the caller how to read more using `offset` and `limit`. The notice is part of the contract: callers rely on it to know when to paginate.
- `offset` and `limit` together select a half-open line range. `offset` past the end of the file returns no lines and a notice describing the actual file length.
- An empty file returns an empty body together with a notice that the file exists but has no content. (Distinguishes "we read it" from "the file is missing".)
- File-type-specific behaviour:
  - **Text files**: contents with line numbers as above.
  - **Images** (PNG, JPG, GIF, …): the upstream tool returns the image as visual content the model can see, downscaling large images to fit the model's image-size limits. Whether this server emits visual content or a textual placeholder is an open implementation question; see [Known gaps](#known-gaps).
  - **PDFs**: short PDFs are returned whole. Longer documents are read in `pages` ranges up to 20 pages per call.
  - **Jupyter notebooks** (`.ipynb`): the upstream tool returns all cells together with their outputs (code, markdown, and visualisations). Whether this server provides notebook handling is an open implementation question; see [Known gaps](#known-gaps).
- Directories are not readable: the tool returns an error directing the caller to list directory contents another way (typically via the shell tool).

## Edge cases and constraints

- A read that passes an explicit `offset` or `limit` but still exceeds the per-tool token budget is an error. The upstream tool surfaces this as a recoverable error and asks the caller to narrow the window.
- Line truncation: very long lines may be truncated to a fixed character cap and a notice added. The cap value is an implementation parameter that should match the upstream behaviour observed for the targeted CLI version.
- Symlinks are followed transparently for the read itself. Whether allow/deny rules apply to the symlink path, the target path, or both is a permission-system question (see [Permissions and security](#permissions-and-security)).
- Re-reading a file that has changed on disk since a previous read is fine; the result reflects current content. Editing after such a change is governed by [`Edit`](./edit.md)'s pre-check, not by `Read`.

## Error behaviour

The exact error string format is **TBD**. The upstream documentation describes failures qualitatively ("returns an error") but does not pin down the string. The implementation will choose a stable format that:

- distinguishes the file-does-not-exist case from the path-is-a-directory case,
- distinguishes a permission failure from an I/O failure,
- includes the requested path verbatim so the model can correct its input.

When the implementation lands, this section is updated with the concrete format and matched against observed CLI behaviour.

## Permissions and security

- The upstream `Read` tool does **not** require a permission prompt in the default permission model. It is treated as a read-only operation.
- The upstream permission system supports `Read(<path-pattern>)` rules that constrain which paths are readable, with gitignore-style anchoring. The same `Read(...)` rules are applied as a best-effort filter to other read-shaped tools, including `Grep` and `Glob`.
- This MCP server is **sandbox-agnostic**. It does not enforce path restrictions, redact contents, or apply allow/deny rules of its own. Those decisions belong to whatever environment installs the server (a Docker container with a bind-mounted workspace, a microVM, a remote VM, …). The expected operational pattern is to give the server filesystem access to exactly the directories the agent is allowed to touch.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Visual content for images.** The upstream `Read` tool can return image data the model sees as a picture. Doing the same here multiplies the token cost in transports that serialise structured tool output to text. The decision (visual content, text placeholder with metadata, or selectable via configuration) is deferred to the `Read` implementation PR.
- **PDF and Jupyter notebook handling.** The upstream tool ships dedicated branches for both. Whether this server implements those branches in the initial release, defers them, or surfaces a placeholder is an open implementation question.
- **Line and character caps.** The exact numeric defaults (default line cap, per-line truncation cap) are not published. The implementation will pick defaults that match observed CLI behaviour for the pinned CLI version; the chosen values will be recorded here at that point.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Read tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Read and Edit section), [`how-claude-code-works`](https://code.claude.com/docs/en/how-claude-code-works).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-readfile.md`, `tool-description-readfile-compact.md`, `system-reminder-read-truncation-retry-guidance.md`, plus the cluster of `system-reminder-*` files that describe the notices appended to truncated, empty, modified-since-read, and offset-past-end reads.
- `1rgs/nanocode`: the `read` tool implementation, used as a minimal independent cross-check for line numbering and the empty/truncated notice strings.
