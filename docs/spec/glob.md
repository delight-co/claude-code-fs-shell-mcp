# Glob

## Purpose

Find files by name pattern. Where [`Grep`](./grep.md) looks inside files, `Glob` looks at the directory tree and returns paths that match a glob expression.

## Signature

```json
{
  "name": "glob",
  "input_schema": {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "Glob pattern, supporting standard syntax including `**` for recursive match."
      },
      "path": {
        "type": "string",
        "description": "Absolute path to the directory to search. Defaults to the working directory."
      }
    },
    "required": ["pattern"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names are stable across the captured CLI versions.

## Semantics

- Standard glob syntax. `**` matches any number of intermediate directories, `*` matches anything except a path separator, and `{a,b}` brace expansion is supported. Examples:
  - `**/*.go` — every `.go` file at any depth.
  - `src/**/*.ts` — every `.ts` file under `src/`.
  - `*.{json,yaml}` — `.json` and `.yaml` files directly in the search root.
- Results are sorted by **modification time** with the most recently modified file first.
- Results are **capped** at a fixed number of paths. When the cap is hit, the result includes a flag telling the caller the list was truncated so it can narrow the pattern. The upstream cap is **100 paths**.
- By default, `.gitignore` is **not** respected and gitignored files appear alongside tracked ones. This is the opposite of [`Grep`](./grep.md)'s default. The upstream CLI exposes an environment toggle to make `Glob` respect `.gitignore`.
- If `path` is omitted, the search root is the working directory.

## Edge cases and constraints

- A `pattern` that matches nothing is a successful empty result, not an error.
- A `path` that does not exist or is unreadable is an error.
- Hidden files and directories (entries beginning with `.`) follow the underlying glob library's default. If the implementation deviates from that default, the deviation is documented.
- Symbolic links are followed transparently. Loop protection is the responsibility of the glob library.
- The mtime sort uses each file's modification time at the moment the result is built. Files created or touched during the search may sort to the top.

## Error behaviour

Concrete error string formats are **TBD**. The implementation will distinguish:

- invalid pattern (with the pattern and the parser message),
- path-not-found or path-unreadable,
- result-cap exceeded (signalled as a truncation flag on a success result, not as an error).

## Permissions and security

- The upstream `Glob` tool does **not** require a permission prompt; it is treated as read-only. Allow/deny rules from the `Read(<path-pattern>)` family are applied as a best-effort filter.
- This MCP server is **sandbox-agnostic**. It does not apply allow/deny rules of its own. The hosting environment is expected to constrain which paths the agent can see.

## Implementation status

🔴 Not started. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Hidden-file behaviour.** Whether the implementation matches dotfiles by default depends on the underlying glob library. The choice (and any deviation from the library default) will be recorded here at implementation time.
- **gitignore-respecting mode.** The upstream tool exposes an environment toggle to make `Glob` respect `.gitignore`. Whether this server implements an equivalent (configuration flag, environment variable, …) is an implementation decision.
- **Result cap.** The default cap value will match the upstream **100 paths** unless a reason to deviate emerges. Any deviation will be recorded here.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (Glob tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (Read and Edit section, including the note that `Read(...)` rules apply to `Glob` on a best-effort basis).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-glob.md`, `tool-description-glob-compact.md`.
- `1rgs/nanocode`: the `glob` tool implementation, used as an independent cross-check for the mtime sort and the truncation behaviour.
