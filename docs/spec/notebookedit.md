# NotebookEdit

## Purpose

Modify a Jupyter notebook (`.ipynb`) one cell at a time. The tool targets cells by their `cell_id` and supports three operations: replace a cell's source, insert a new cell after a target cell, or delete a target cell. Unlike [`Edit`](./edit.md), the tool does not do byte-exact string replacement across the notebook file; cells are addressed structurally.

## Status

> **Deferred.**
>
> This tool is in scope for the project's specification because the upstream `NotebookEdit` is a first-class built-in and an agent that depends on it should be able to migrate behaviour-preserving. It is **out of scope for the initial implementation milestones**: notebook editing is not the primary use case our early adopters need, and the round-trip parsing of `.ipynb` introduces enough surface area that it deserves its own design pass.
>
> The spec below is a placeholder. It captures what is publicly known about the tool so the gap is visible, and so picking the work up later does not start from zero.

## Signature

```json
{
  "name": "notebook_edit",
  "input_schema": {
    "type": "object",
    "properties": {
      "notebook_path": {
        "type": "string",
        "description": "Absolute path to the `.ipynb` notebook file."
      },
      "cell_id": {
        "type": "string",
        "description": "Identifier of the cell to operate on. Optional for `insert` at the start of the notebook."
      },
      "new_source": {
        "type": "string",
        "description": "New cell source. Required for `replace` and `insert`."
      },
      "cell_type": {
        "type": "string",
        "enum": ["code", "markdown"],
        "description": "Cell type for `insert`."
      },
      "edit_mode": {
        "type": "string",
        "enum": ["replace", "insert", "delete"],
        "default": "replace"
      }
    },
    "required": ["notebook_path"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names mirror those visible in the prompt-level descriptions.

## Semantics

- `replace` (the default): overwrite the source of the cell identified by `cell_id`.
- `insert`: add a new cell *after* the cell identified by `cell_id`. If `cell_id` is omitted, the new cell is placed at the start of the notebook. `cell_type` (`code` or `markdown`) is required in this mode.
- `delete`: remove the cell identified by `cell_id`.

Reading notebooks happens through [`Read`](./read.md), which the upstream tool returns as the set of cells with their outputs (code, markdown, and visualisations) so the caller has the `cell_id` values to address.

## Edge cases and constraints

- A `cell_id` that does not exist in the notebook is an error.
- `insert` without `cell_type` is an error.
- The encoding of the notebook file (the JSON envelope around cells) must be preserved on write; the tool is not free to reformat the surrounding JSON, change `nbformat` versions, or strip metadata.
- Outputs and metadata attached to a cell are preserved across `replace` (the source changes, the cell identity does not). Deletions remove outputs along with the cell.

## Error behaviour

**TBD.** Pending implementation.

## Permissions and security

- The upstream `NotebookEdit` tool requires a permission prompt by default. It shares the `Edit(<path-pattern>)` rule family with `Write` and `Edit`.
- This MCP server is **sandbox-agnostic** and does not apply allow/deny rules of its own.

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **No implementation timeline.** This tool is intentionally out of scope for the initial release window. Promoting it to "in scope" is a deliberate decision, not a default, and will be recorded in the matrix when it happens.
- **`nbformat` compatibility.** The exact `nbformat` versions the upstream tool supports are not published. The implementation, when it lands, will document its supported range.
- **Outputs and metadata handling on replace.** The upstream tool preserves them but does not publish the exact policy (do `execution_count`, `cell.metadata.collapsed`, etc., persist?). The implementation will pick a policy and record it here.

## Source notes

- Official documentation: [`tools-reference`](https://code.claude.com/docs/en/tools-reference) (NotebookEdit tool behavior section), [`permissions`](https://code.claude.com/docs/en/permissions) (matchable parameters section: `notebook_path`).
- `Piebald-AI/claude-code-system-prompts` @ `b6d6be0`: `tool-description-notebookedit.md`.
- `1rgs/nanocode`: does not implement notebook editing; no independent cross-check is available for this tool.
