# TaskOutput

## Purpose

Fetch the current output of a previously started background task by `task_id`, optionally blocking until the task completes. This tool is the read side of the background-task surface that pairs with [`Bash`](./bash.md)'s `run_in_background` mode and the [`Monitor`](./monitor.md) tool: the agent starts a task, receives a `task_id`, and uses `TaskOutput` to read the output produced so far.

## Status

> **Deferred.**
>
> This tool is in scope for the project's specification because the upstream `TaskOutput` is a first-class built-in that an agent depends on to keep working with long-running shell tasks. It is **out of scope for the initial implementation milestones**: the initial milestones aim at the foreground filesystem and shell workflow; the background-task tool family lands once `Bash` itself is stable and the per-session task registry can be exercised in real use.
>
> The spec below records what is publicly known about the tool so the gap is visible, and so picking the work up later does not start from zero.

## Signature

```json
{
  "name": "task_output",
  "input_schema": {
    "type": "object",
    "properties": {
      "task_id": {
        "type": "string",
        "description": "Identifier of the task to fetch output for."
      },
      "block": {
        "type": "boolean",
        "default": true,
        "description": "When `true`, wait up to `timeout` milliseconds for the task to finish before returning."
      },
      "timeout": {
        "type": "integer",
        "minimum": 0,
        "maximum": 600000,
        "default": 30000,
        "description": "Maximum wait time in milliseconds when `block` is `true`."
      }
    },
    "required": ["task_id"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names and the defaults above mirror the prompt-level descriptions and are stable across the captured CLI versions. The upstream tool is **deprecated** in favour of `Read` on the task's output file path, though it remains in the prompt-level surface for backward compatibility.

## Semantics

- `task_id` addresses an entry in the per-session task registry seeded by a background-task start (most commonly by `Bash` with `run_in_background: true`, but other tools may also seed the registry).
- `block: true` waits up to `timeout` milliseconds. If the task completes within that window, the result includes the final output and an exit code. If it does not, the result includes whatever output has accumulated so far.
- `block: false` returns immediately with whatever output is available.
- The output is the same byte stream that the task wrote to its output file (with the same `[stderr] ` prefix interleaving applied after spill — see [`Bash`](./bash.md) for the underlying output model).

## Error behaviour

**TBD.** Pending implementation. The wording the upstream tool advertises for the most common conditions includes:

- Missing `task_id`: `Task ID is required`
- Unknown `task_id`: `No task found with ID: <task_id>`

The implementation will pin its own wording at impl time and record the formats here.

## Permissions and security

- The upstream `TaskOutput` tool does **not** require a permission prompt by default: it only reads task-local output that the agent itself started.
- This MCP server is **sandbox-agnostic**: when implemented, the server will expose `TaskOutput` against the per-session task registry it maintains for `Bash` background tasks.

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Block / poll semantics.** The exact behaviour when `block: true` and the task is already complete by the time the call arrives is not pinned in public documentation. The implementation, when it lands, will document its choice.
- **Output framing across spill.** Whether the call returns the entire output history or a delta from the last `TaskOutput` call is not pinned in public documentation. The implementation will pick a policy and record it here.
- **Caller's preferred surface.** The upstream tool description hints at `Read` on the task's output file path as the preferred way to consume the output. The MCP server will document the same preference in its own copy of the description, so callers route to the lighter `Read` path when possible.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (background task surface section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0`: the `tool-description-taskoutput-*` cluster.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
