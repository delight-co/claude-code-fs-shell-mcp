# Grep

## Purpose

Search file contents for patterns. Where [`Glob`](./glob.md) finds files by name, `Grep` finds lines inside them. The tool is the agent's primary instrument for code search: a regex, an optional file scope, an output mode, and (for the content mode) the usual surrounding-lines controls.

## Signature

```json
{
  "name": "grep",
  "input_schema": {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "Regular expression to search for, in ripgrep syntax."
      },
      "path": {
        "type": "string",
        "description": "Absolute path to a file or directory to search. Defaults to the working directory."
      },
      "glob": {
        "type": "string",
        "description": "File-name glob to narrow the search (e.g. `**/*.tsx`)."
      },
      "type": {
        "type": "string",
        "description": "ripgrep file-type name to narrow the search (e.g. `go`, `py`)."
      },
      "output_mode": {
        "type": "string",
        "enum": ["files_with_matches", "content", "count"],
        "default": "files_with_matches"
      },
      "-A": { "type": "integer", "description": "Lines of trailing context (content mode only)." },
      "-B": { "type": "integer", "description": "Lines of leading context (content mode only)." },
      "-C": { "type": "integer", "description": "Lines of context on both sides (content mode only)." },
      "-n": { "type": "boolean", "description": "Include line numbers (content mode only)." },
      "-i": { "type": "boolean", "description": "Case-insensitive match." },
      "multiline": {
        "type": "boolean",
        "description": "Allow the pattern to match across line boundaries.",
        "default": false
      },
      "head_limit": {
        "type": "integer",
        "description": "Return only the first N lines of output."
      }
    },
    "required": ["pattern"]
  }
}
```

The flag-style names (`-A`, `-B`, `-C`, `-n`, `-i`) match what the model already knows from ripgrep and from the prompt-level descriptions; they are not invented by this spec.

## Semantics

- The search engine is **ripgrep** and the regex syntax is ripgrep's. Regex metacharacters that the caller wants literal need escaping (for example, finding `interface{}` in Go takes the pattern `interface\{\}`).
- `output_mode`:
  - `files_with_matches` (the default): one file path per match, no line content.
  - `content`: matching lines, each prefixed with file path and (with `-n`) line number. Honours `-A`, `-B`, `-C` for surrounding context.
  - `count`: one line per file with a match, containing the file path and the number of matches.
- `glob` and `type` narrow the search scope. They can be combined.
- `path` is the search root. If omitted, the search runs from the working directory.
- By default, `.gitignore` is respected and ignored files are skipped. To search a file that is gitignored, the caller passes its path directly.
- `multiline: true` enables `--multiline --multiline-dotall` semantics: the pattern is allowed to cross line boundaries.
- `head_limit` truncates the result to the first N lines (after any internal formatting).

## Edge cases and constraints

- An invalid regex is a tool-level error, not an empty result.
- A `path` that does not exist or is unreadable is an error.
- A search that produces no matches is a successful empty result, not an error.
- The implementation may impose an upper bound on the total number of lines returned (independent of `head_limit`) to keep results model-friendly. The bound is documented in the README.
- Binary file handling follows ripgrep defaults: most binary files are skipped unless the caller forces a binary search through ripgrep flags exposed by this tool.

## Error behaviour

Concrete error string formats are **TBD**. The implementation will distinguish:

- invalid regex (with the pattern and the parser message),
- path-not-found or path-unreadable,
- output overflow (if the implementation caps total output),
- ripgrep failure (with the underlying exit code and stderr preview).

## Permissions and security

- The upstream `Grep` tool does **not** require a permission prompt; it is treated as read-only. Allow/deny rules from the `Read(<path-pattern>)` family are applied as a best-effort filter.
- This MCP server is **sandbox-agnostic**. It does not apply allow/deny rules of its own. The hosting environment is expected to constrain which paths the agent can see.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **ripgrep version pin.** The implementation will pin or document a minimum ripgrep version. Some flags this spec lists rely on ripgrep features that are version-gated.
- **Result cap.** The implementation will pick a default upper bound on returned lines (independent of `head_limit`) and document it. The upstream tool's exact cap is not published.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Grep tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Read and Edit section, including the note that `Read(...)` rules apply to `Grep` and `Glob` on a best-effort basis).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-grep.md`, `tool-description-grep-compact.md`.
- `1rgs/nanocode`: the `grep` tool implementation, used as an independent cross-check for the default output mode and the empty-result semantics.
