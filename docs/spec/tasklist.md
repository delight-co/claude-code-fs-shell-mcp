# TaskList

## Purpose

List the background tasks currently tracked by the session. Returns a summary of each task (`task_id`, `status`, originating `command`, etc.) so the caller can choose a `task_id` to inspect with [`TaskOutput`](./taskoutput.md), look up in detail with [`TaskGet`](./taskget.md), or terminate with [`TaskStop`](./taskstop.md).

## Status

> **Deferred.** Same reasoning as for [`TaskOutput`](./taskoutput.md). The background-task tool family lands once `Bash` itself is stable.

## Signature

**TBD.** The upstream tool's parameter schema is not yet pinned in this spec. The implementation, when it lands, will mirror the upstream signature observed at that point in time.

A plausible shape, based on the surrounding tool family:

```json
{
  "name": "task_list",
  "input_schema": {
    "type": "object",
    "properties": {
      "status": {
        "type": "string",
        "description": "Optional filter by task status (e.g. running / completed / killed)."
      }
    }
  }
}
```

## Semantics

- The result is a snapshot of the per-session task registry at the time of the call.
- Each entry includes at least `task_id`, `status`, the originating `command`, and the path to the task's output file.
- Optional filtering (e.g. by status) is part of the surface; the exact set of filters is pending observation.

## Error behaviour

**TBD.** Pending implementation. A `TaskList` call against a session with no tasks is **not** an error: the result is an empty list.

## Permissions and security

- The upstream `TaskList` tool does **not** require a permission prompt by default: it surfaces metadata about tasks the session itself owns.
- This MCP server is **sandbox-agnostic**: when implemented, the server will return entries from the per-session task registry it maintains.

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Full signature pinning.** The upstream parameter set, including optional filters, is pending observation.
- **Result entry shape.** The exact fields returned per task (in particular which timestamps and which derived state) are pending observation.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (background task surface section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0`: the `tool-description-tasklist-*` files (when present).

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
