# Monitor

## Purpose

Watch an external signal — a shell command's output stream, or a WebSocket connection — and notify the agent on each event without requiring the agent to poll. `Monitor` lets the agent ask "tell me when something interesting happens" instead of "run this every N seconds until it does." The intended pattern is: start a Monitor, keep working on other things, and let the event notifications interrupt that work when they arrive.

## Status

> **Deferred.**
>
> This tool is in scope for the project's specification because the upstream `Monitor` is a first-class built-in that an agent uses to avoid sleep loops and reduce wasted tool calls. It is **out of scope for the initial implementation milestones**: the event-push transport (server-sent events or equivalent) and the per-session monitor registry require their own design pass, which is best done after `Bash` is stable.
>
> The spec below records what is publicly known about the tool so the gap is visible, and so picking the work up later does not start from zero.

## Signature

```json
{
  "name": "monitor",
  "input_schema": {
    "type": "object",
    "properties": {
      "description": {
        "type": "string",
        "description": "Short description of what is being monitored (a few words). Surfaced in UI rows."
      },
      "timeout_ms": {
        "type": "integer",
        "minimum": 1000,
        "default": 300000,
        "description": "Maximum time to keep the monitor alive when `persistent` is false. Default 5 minutes."
      },
      "persistent": {
        "type": "boolean",
        "default": false,
        "description": "When `true`, the monitor runs until a [`TaskStop`](./taskstop.md) call or the end of the session, ignoring `timeout_ms`."
      },
      "command": {
        "type": "string",
        "description": "Shell command whose output stream should be observed. Mutually exclusive with `ws`."
      },
      "ws": {
        "type": "object",
        "properties": {
          "url": { "type": "string" },
          "protocols": { "type": "array", "items": { "type": "string" } }
        },
        "description": "WebSocket connection to observe. Mutually exclusive with `command`."
      }
    },
    "required": ["description"]
  }
}
```

The upstream tool does not publish a JSON schema. Parameter names and defaults mirror the prompt-level descriptions.

## Semantics

- Exactly one of `command` and `ws` must be provided.
- The synchronous result of a `Monitor` call confirms that the monitor has started and returns a `task_id` plus a message instructing the caller not to poll. The actual events arrive asynchronously, surfaced through the same event-notification channel that other long-running tools use.
- When `persistent: false`, the monitor self-terminates at `timeout_ms` and the agent is notified of the termination.
- When `persistent: true`, the monitor runs until [`TaskStop`](./taskstop.md) is called with its `task_id`, or until the session ends.

### Synchronous start message

The synchronous response advises the agent to keep working rather than poll. The wording the upstream advertises includes the literal phrase "Keep working — do not poll or sleep. Events may arrive while you are waiting for the user — an event is not their reply." The MCP server, when implemented, will mirror this guidance verbatim so callers behave the same way.

## Error behaviour

**TBD.** Pending implementation. The constraint that exactly one of `command` and `ws` is provided is enforced at schema validation time; other failure modes (invalid `command` shape, `ws.url` unreachable, etc.) are surfaced as tool errors.

## Permissions and security

- The upstream `Monitor` tool's permission model depends on what is being monitored: a `command` monitor inherits the same permission semantics as [`Bash`](./bash.md), and a `ws` monitor inherits the same semantics as outbound network access in the hosting environment.
- This MCP server is **sandbox-agnostic**: when implemented, the server will apply the same permission deferral as the other tools (no allow/deny rules of its own; the hosting environment is responsible).

## Implementation status

❌ Deferred. See [README](./README.md) for the project-wide matrix.

## Known gaps

- **Event-push transport.** The exact mechanism the upstream tool uses to push events back to the model (server-sent events, JSON-RPC notifications, etc.) is not pinned in public documentation. The implementation will pick a transport that fits the MCP Streamable HTTP context and record the choice here.
- **WebSocket monitor scope.** The exact set of WebSocket events that are surfaced (`open` / `message` / `close` / `error`) is pending observation.
- **Per-session monitor cap.** Whether the upstream tool caps the number of simultaneous monitors per session is pending observation. The MCP server will pick a sensible default and record it here.

## Source notes

- Claude Code CLI [official documentation](https://code.claude.com/docs/en/): `tools-reference` (Monitor tool behavior section).
- [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) @ commit `b6d6be0`: the `tool-description-monitor-*` cluster and `tool-description-background-monitor-streaming-events.md`.

Verified against Claude Code CLI v2.1.195 on 2026-06-27.
