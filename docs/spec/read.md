# Read

## Purpose

Read the contents of a file from the local filesystem and return them in the same shape a Claude Code CLI agent expects from its built-in `Read` tool: text with line numbers for plain text, paginated slices for PDFs, structured cells with outputs for Jupyter notebooks, visual image content for images, and a small set of well-defined notices when the file cannot be returned as plain content (empty, shorter than the offset, truncated for size).

The tool is the canonical entry point for an agent to ground itself in a file before editing it. Its read-before-edit contract underpins both [`Write`](./write.md) and [`Edit`](./edit.md).

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
        "description": "PDF-only page range, for example `\"1-5\"` or `\"3\"`. Up to 20 pages per call; required for PDFs longer than 10 pages."
      }
    },
    "required": ["file_path"]
  }
}
```

The Claude Code CLI does not publish a JSON schema directly. The parameter names, types, and required/optional markers above mirror the wording the model sees in the prompt-level descriptions and are stable across the captured CLI versions.

## Semantics

### Path handling

- `file_path` must be an absolute path. Relative paths are rejected without reading the file.
- A non-existent file produces an error.
- A path that resolves to a directory produces an error directing the caller to use the shell tool to list directory contents.
- Symlinks are followed transparently for the read itself. Allow/deny semantics around symlinks are a permission-system concern; see [Permissions and security](#permissions-and-security).

### Line cap and pagination

- The tool returns up to `MAX_LINES_CONSTANT` lines by default, starting at the first line. The default value is **2000**, matching the value the built-in tool advertises in its prompt-level descriptions. The implementation exposes this value through configuration (environment variable or settings file) so it can be tuned without rebuilding the server.
- `offset` is **1-based**: `offset: 1` returns the file starting at the first line. `limit` is the maximum number of lines to return.
- Without `offset` and `limit`, the response is the file from line 1 up to `MAX_LINES_CONSTANT` lines, plus the truncation notice (see [Error and notice behaviour](#error-and-notice-behaviour)) when the file is longer.
- With explicit `offset` or `limit`, the response is the requested window. A window that begins past the end of the file returns the offset-past-end notice rather than an error.

### Line formatting

- Returned text is prepended with line numbers in `cat -n` style (right-aligned line number, a tab, then the line). This is the format the Claude Code CLI built-in uses and the format `Edit` expects callers not to mistake for content.
- Very long lines may be truncated to a fixed character cap before being returned. The cap is an implementation parameter; the implementation documents the chosen value alongside the configurable `MAX_LINES_CONSTANT`.

### File-type handling

The tool dispatches on the detected content type. Each branch matches the built-in tool's observable behaviour.

#### Plain text

Returned as line-numbered text, subject to the line cap and pagination rules above.

#### Images (PNG, JPG, GIF, …)

Returned as **visual content**, not as raw bytes or base64 in a text body. Concretely:

- The image is returned in the MCP response as an `image` content block (the MCP `image` content type), with the correct `mimeType` set.
- The image arrives once, as a first-class image content block. The server never duplicates the image data into `structuredContent`; see [Common conventions / Response transport](./README.md#response-transport).
- The server may downscale very large images before transmission to keep the response within the model's image-size limits, matching the built-in tool's behaviour.

#### PDFs

- PDFs are returned in slices addressed by the `pages` parameter (for example `"1-5"` or `"3"`).
- A single call returns at most **20 pages**.
- For PDFs longer than **10 pages**, `pages` is required; calling without it on a long PDF is an error.
- The exact transport of PDF content to the model (whether each page is rendered to an image content block, whether the model receives an extracted text representation, or some combination) depends on the upstream Claude Code CLI's internal handling, which is not fully described in public documentation. The implementation will match observed behaviour for the pinned CLI version; see [Known gaps](#known-gaps).

#### Jupyter notebooks (`.ipynb`)

Returned as the notebook's cells together with their outputs. The structure mirrors the built-in tool: code cells, markdown cells, and visualisations are all surfaced, with the cell identifiers preserved so a subsequent [`NotebookEdit`](./notebookedit.md) can address them.

## Error and notice behaviour

The Claude Code CLI distinguishes between two kinds of out-of-band content:

- **Notices** that are not errors and are returned alongside (or in place of) the file body. These are emitted as literal text matching the strings the CLI uses, so a client that recognises them as system reminders treats them the same way it treats the built-in tool's output.
- **Errors** that fail the call. The exact wording of error messages is not pinned by the upstream documentation. The implementation chooses formats that distinguish the cases below and includes the requested path verbatim so the caller can correct it.

### Notices the server emits

| Condition | Literal text returned (verbatim) |
| --------- | -------------------------------- |
| File exists but has no content | `<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>` |
| `offset` past end of file | `<system-reminder>Warning: the file exists but is shorter than the provided offset (N). The file has M lines.</system-reminder>` (with `N` = requested offset, `M` = total line count) |
| File body truncated to fit the line cap | `Note: The file <filename> was too large and has been truncated to the first 2000 lines. Don't tell the user about this truncation. Use Read to read more of the file if you need.` (`2000` reflects the configured `MAX_LINES_CONSTANT`) |
| Server-side response exceeds the MCP output budget | `[OUTPUT TRUNCATED - exceeded N token limit]` followed by the standard "if this MCP server provides pagination …" guidance |

These are returned as text content in the MCP response (the `image` branch above runs in addition, not instead). The strings above are byte-exact with the upstream CLI's wording for the pinned CLI version. When the upstream wording changes, this section is updated as part of the spec refresh.

### Errors the server returns

The following conditions are surfaced as MCP tool errors rather than as content. The error envelope distinguishes the case so the caller can react.

- Path is relative (must be absolute).
- File does not exist.
- Path resolves to a directory.
- Permission denied at the OS level.
- I/O failure while reading.
- PDF longer than 10 pages called without a `pages` parameter.
- Read window (after `offset`/`limit`) still exceeds the per-tool token budget.

Each error message includes the requested path and, where relevant, the offending parameter so the model can correct the call.

## Edge cases and constraints

- An empty `file_path` is an error before any I/O happens.
- An `offset` of 0 (i.e. less than the documented minimum of 1) is an error.
- Re-reading a file that has changed on disk since a previous read returns the current contents. The freshness-check contract that constrains [`Write`](./write.md) and [`Edit`](./edit.md) is theirs, not `Read`'s.
- Binary file detection: image branches dispatch on the detected MIME type rather than on the file extension alone. PDF and Jupyter notebook dispatch use the same detected type plus the canonical extensions, so a renamed file is handled correctly.

## Permissions and security

- The upstream `Read` tool is read-only and does **not** require a permission prompt in the Claude Code CLI's default permission model.
- The CLI's permission system supports `Read(<path-pattern>)` rules with gitignore-style path anchoring. Those rules are applied as a best-effort filter to other read-shaped tools, including [`Grep`](./grep.md) and [`Glob`](./glob.md).
- This MCP server is **sandbox-agnostic**: it does not enforce path restrictions, redact contents, or apply allow/deny rules of its own. Those decisions belong to whatever environment installs the server. The expected operational pattern is to give the server filesystem access to exactly the directories the agent is allowed to touch.

## Implementation status

🟢 **Drafted.** The spec is essentially complete from public sources; remaining open points (see [Known gaps](#known-gaps)) are flagged and will be resolved through observation against the pinned Claude Code CLI version during the implementation pull request. The implementation itself has not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

These are gaps the implementation pull request will close, either by choosing a concrete behaviour or by reporting observed behaviour against the pinned CLI version.

- **PDF transport details.** The mechanism by which the upstream tool conveys PDF page content to the model — whether each page is rendered to an image content block, whether an extracted text representation is included, or some combination — is not described in detail in public documentation. The implementation will match the observed behaviour for the pinned CLI version and document the chosen approach here at that point.
- **Long-line truncation cap.** The exact character cap at which an individual line is truncated is not published. The implementation will pick a value matching observed behaviour and document it.
- **Error message string formats.** The upstream documentation specifies which conditions are errors but not the exact wording. The implementation will choose a stable wording that distinguishes the cases listed in [Errors the server returns](#errors-the-server-returns) and document the formats here.

## Known limitations

The following behaviours of the upstream Claude Code CLI's built-in `Read` cannot be reproduced by an MCP server interposed between the CLI and the filesystem. They are recorded here so callers know which built-in behaviours are not available through this server.

- **Out-of-band system reminders that depend on the client tracking external state.** The upstream CLI emits notices for several conditions that the CLI itself observes outside of any single tool call — for example, "the file was modified by the user or a linter since you last read it", "the user opened this file in their IDE", and the guidance pushed at the model when a turn's accumulated reads exceed an internal budget. Reproducing these requires the CLI to inject text into the model's context as a side effect of file-system state. There is no MCP-level mechanism for our server to drive that injection: the upstream CLI is closed and proprietary, and we cannot modify it to accept out-of-band reminders from this server. Making our server stateful or adding file watchers does not change this — the missing piece is the client-side hook, not the server-side observation. The agent therefore loses these client-tracking notices when its filesystem operations are routed through this server.

## Source notes

- Claude Code CLI official documentation:
  - [`tools-reference`](https://code.claude.com/docs/en/tools-reference) — Read tool behavior section.
  - [`permissions`](https://code.claude.com/docs/en/permissions) — Read and Edit section.
  - [`how-claude-code-works`](https://code.claude.com/docs/en/how-claude-code-works).
- `Piebald-AI/claude-code-system-prompts` @ commit `b6d6be0` (Claude Code CLI v2.1.191, 2026-06-24):
  - Tool descriptions: `tool-description-readfile.md`, `tool-description-readfile-compact.md`.
  - System reminders the server emits: `system-reminder-file-exists-but-empty.md`, `system-reminder-file-shorter-than-offset.md`, `system-reminder-file-truncated.md`, `system-reminder-mcp-output-truncation-warning.md`.
  - System reminders the server cannot emit (recorded under [Known limitations](#known-limitations)): `system-reminder-read-truncation-retry-guidance.md`, `system-reminder-file-modified-by-user-or-linter.md`, `system-reminder-file-modification-detected-budget-exceeded.md`, `system-reminder-file-opened-in-ide.md`, `system-reminder-file-summary-completeness-disclosure.md`, `system-reminder-large-file-full-content-reading-guidance.md`, `system-reminder-large-pdf-read-guidance.md`.
- `1rgs/nanocode`: the `read` tool implementation, used as an independent cross-check for line numbering, the 1-based offset, and the empty/truncated notice shapes.
