# TaskGet

## Purpose

Fetch the registry entry for a single background task by `task_id`. Where [`TaskList`](./task-list.md) returns a summary of every task in the session, `TaskGet` returns the full record for one. Where [`TaskOutput`](./task-output.md) returns the *output* the task produced, `TaskGet` returns its *metadata* (status, exit code, timestamps, the originating command, the output file path).

## Status

> **Deferred.** Same reasoning as for [`TaskOutput`](./task-output.md). The background-task tool family lands once `Bash` itself is stable.

## Signature

```json
{
  "name": "task_get",
  "input_schema": {
    "type": "object",
    "properties": {
      "task_id": {
        "type": "string",
        "description": "Identifier of the task to fetch the registry entry for."
      }
    },
    "required": ["task_id"]
  }
}
```

The upstream tool does not publish a JSON schema. The single-parameter shape mirrors the prompt-level descriptions.

## Semantics

- The result is a snapshot of the named task's registry entry at the time of the call.
- The entry includes at least `task_id`, `status` (`running` / `completed` / `killed` / similar), the originating `command`, the output file path, start and end timestamps when available, and exit code when the task has completed.
- A `TaskGet` call does **not** wait for the task to complete; the result reflects the registry state at the moment of the call. For waiting, use [`TaskOutput`](./task-output.md) with `block: true`.

## Error behaviour

**TBD.** Pending implementation. Likely conditions include:

- Missing `task_id`
- Unknown `task_id`

## Permissions and security

- The upstream `TaskGet` tool does **not** require a permission prompt by default: it surfaces metadata about tasks the session itself owns.
- This MCP server is **sandbox-agnostic**: when implemented, the server will return entries from the per-session task registry it maintains.

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Full result entry shape.** The exact fields returned (in particular which timestamps and which derived state) are pending observation.
- **Behaviour on tasks that have been evicted.** Whether the call returns an explicit "evicted" status or treats the entry as "not found" is pending observation.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (background task surface section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0`: the `tool-description-taskget-*` files (when present).

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
