# TaskStop

## Purpose

Stop a background task by `task_id`. This is the kill side of the background-task surface that pairs with [`Bash`](./bash.md)'s `run_in_background` mode: the agent started a task, decided it no longer wants the result (or wants to free the underlying shell process), and uses `TaskStop` to terminate the task and release any resources it holds.

## Status

> **Deferred.** Same reasoning as for [`TaskOutput`](./taskoutput.md). The background-task tool family lands once `Bash` itself is stable.

## Signature

```json
{
  "name": "task_stop",
  "input_schema": {
    "type": "object",
    "properties": {
      "task_id": {
        "type": "string",
        "description": "Identifier of the task to stop."
      },
      "shell_id": {
        "type": "string",
        "description": "Deprecated: use task_id instead."
      }
    }
  }
}
```

Either `task_id` or `shell_id` must be provided; if both are present, `task_id` wins. The `shell_id` parameter is retained as an alias for backward compatibility with earlier upstream releases.

The upstream tool exposes aliases (`KillShell`, `KillBash`) for the same handler, reflecting that callers used different mental models for the same operation.

## Semantics

- The named task is terminated through the same process-group kill path that `Bash` uses internally (SIGTERM to the process group, followed by SIGKILL if the task does not exit within a grace period).
- The task registry entry is marked `status: "killed"` and an end timestamp is recorded.
- The output file the task wrote to is **not** automatically removed; it remains available for a later [`TaskOutput`](./taskoutput.md) call (until the registry entry itself is evicted under the session's cleanup policy).

## Error behaviour

**TBD.** Pending implementation. The wording the upstream tool advertises for the most common conditions includes:

- Missing both parameters: `Missing required parameter: task_id`
- Unknown task: `No task found with ID: <id>`
- Task not running (already exited / already killed): `Task <id> is not running (status: <status>)`
- Successful stop: `Successfully stopped task: <task_id> (<command>)`

The implementation will pin its own wording at impl time.

## Permissions and security

- The upstream `TaskStop` tool does **not** require a permission prompt by default: the caller is the same agent that started the task, and the operation is scoped to tasks the session itself owns.
- This MCP server is **sandbox-agnostic**: when implemented, the server will expose `TaskStop` against the per-session task registry it maintains.

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Grace-period and kill-signal exact values.** The upstream grace period and the signal sequence are not pinned in public documentation. The implementation will mirror the values it uses internally for `Bash`'s own timeout-driven kill path.
- **Concurrent `TaskStop` calls against the same task.** Whether a second call observes "already killed" or waits for the first call to complete is not pinned. The implementation will pick a policy and record it here.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (background task surface section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0`: the `tool-description-taskstop-*` cluster.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
