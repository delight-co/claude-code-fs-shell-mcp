# Specification

This directory captures the specification that this MCP server aims to be compatible with: the **filesystem and shell tools built into the Claude Code CLI**. It exists because the value of this project rests on a single claim — that an agent can swap our MCP tools in for the built-ins and behave the same way — and that claim needs to be auditable, not implicit.

The Claude Code CLI is stable but does change occasionally. To make our compatibility window observable, we pin a target version on every spec refresh and bump it deliberately.

## Target

| Field | Value |
| ----- | ----- |
| Claude Code CLI version we target | `v2.1.195` (Read, Write, and Edit specs verified against this version on 2026-06-27; Bash and the remaining tool specs are still pending deepening against this version) |
| Spec last refreshed | 2026-06-27 (Read, Write, and Edit specs verified against v2.1.195) |
| Tools covered | `Read`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `TaskOutput`, `TaskStop`, `TaskList`, `TaskGet`, `Monitor`, `Grep`, `Glob` |

## Sources

The spec is built from publicly available primary sources and a small set of independently developed reference implementations. We do **not** consult any source that derives from the Claude Code CLI's original source code.

| Source | What we use it for |
| ------ | ------------------ |
| Claude Code CLI [official documentation](https://code.claude.com/docs/en/) (`tools-reference`, `permissions`, `permission-modes`, `sandboxing`, `hooks`, `settings`, `security`, `env-vars`, …) | Canonical tool names, permission model, and the observable behaviour Anthropic chooses to publish. |
| [`Piebald-AI/claude-code-system-prompts`](https://github.com/Piebald-AI/claude-code-system-prompts) (pinned at commit `b6d6be0`, which captures Claude Code CLI v2.1.191) | The tool-description text that the CLI sends to the model, including parameter wording, defaults written into the prompt, and the small `system-reminder` strings injected around tool results. |
| [`1rgs/nanocode`](https://github.com/1rgs/nanocode) (a minimal independent Python reference, predating the relevant Claude Code release window) | A small clean-room reference for the essence of each tool, useful when the official docs and the Piebald-AI snapshot leave a behaviour underspecified. |

When the three sources agree, the spec calls a behaviour **established**. When they disagree, or two of them are silent, the spec marks the point as **TBD** and notes which behaviour the implementation chooses.

## Tool matrix

Legend:

| Symbol | Spec coverage | Implementation |
| ------ | ------------- | -------------- |
| ✅ | Established (verified through implementation and observation against the pinned CLI version) | Implemented and exercised by tests |
| 🟢 | Drafted (essentially complete from public sources; awaits implementation-driven verification) | — |
| 🟡 | Skeleton (purpose, signature, headline semantics) | Partial implementation |
| 🔴 | Not started | Not started |
| ❌ | Deferred to a later milestone | Not in scope for the current milestone |

| Tool | Spec | Implementation | Notes |
| ---- | ---- | -------------- | ----- |
| [Read](./read.md)         | 🟢 | 🟡 | Text / image / Jupyter implemented and exercised by unit tests. PDF reading returns a "not yet implemented" error; the PDF branch lands in a follow-up PR after observation. |
| [Write](./write.md)       | 🟢 | 🟡 | Read-before-overwrite, modified-since-read, atomic write, symlink safety, per-session LRU read-tracking. Implementation in place with unit + integration tests; observation against the pinned CLI version still to follow. |
| [Edit](./edit.md)         | 🟢 | 🟡 | Three ordered checks, per-path mutex / symlink safety / atomicity shared with Write. Initial implementation honours exact-substring and `\uXXXX`-escape match strategies; smart-quote normalisation and non-ASCII → escape regex are deferred to follow-up (see Edit spec Known gaps). |
| [NotebookEdit](./notebook-edit.md) | 🟡 | ❌ | Deferred. Out of scope for the initial milestones; spec retained so the gap is visible. |
| [Bash](./bash.md)         | 🟢 | 🔴 | All four design surfaces (cwd persistence, output cap chain, background-task family, read-equivalents detection) pinned. Implementation pending. |
| [TaskOutput](./task-output.md) | 🟡 | ❌ | Deferred. Part of the background-task family that pairs with `Bash`'s `run_in_background` mode; lands after `Bash` itself is stable. |
| [TaskStop](./task-stop.md) | 🟡 | ❌ | Deferred. Part of the background-task family; see `TaskOutput`. |
| [TaskList](./task-list.md) | 🟡 | ❌ | Deferred. Part of the background-task family; see `TaskOutput`. |
| [TaskGet](./task-get.md)   | 🟡 | ❌ | Deferred. Part of the background-task family; see `TaskOutput`. |
| [Monitor](./monitor.md)   | 🟡 | ❌ | Deferred. Event-push tool that pairs with `Bash` and the rest of the background-task family. |
| [Grep](./grep.md)         | 🟢 | 🔴 | Wrapper around ripgrep with the upstream's default args, mode-specific output formatting, pagination, and the byte-exact error / notice wording pinned. |
| [Glob](./glob.md)         | 🟢 | 🔴 | Wrapper around ripgrep's `--files` mode with the upstream's default args, the 100-path cap (25000 in the REPL surface), `--sort=modified`, and the byte-exact error / notice wording pinned. |

## How to read each tool spec

Each file under this directory uses the same structure:

1. **Purpose** — one-paragraph description of what the tool is for.
2. **Signature** — the JSON schema this MCP server intends to expose. Where the upstream signature is unpublished, the schema is reconstructed from the prompt-level descriptions in the Piebald-AI snapshot and cross-checked against the independent reference.
3. **Semantics** — the observable behaviour the tool must implement. Each bullet that pins down a non-trivial behaviour cites which source(s) it comes from.
4. **Edge cases and constraints** — boundary inputs, defaults, and explicit limits.
5. **Error and notice behaviour** — what the tool returns when something fails, and the byte-exact text of the notices it emits in well-defined non-error situations.
6. **Permissions and security** — what the upstream tool requires from a permission system, and how this sandbox-agnostic MCP server relates to it.
7. **Implementation status** — current state, mirroring the matrix above.
8. **Known gaps** — places where the spec is still open and the implementation pull request will close them, either by choosing a behaviour or by reporting observed behaviour.
9. **Known limitations** — behaviours of the upstream built-in that this server cannot reproduce, with the reason.
10. **Source notes** — the specific official-doc URLs and Piebald-AI template files this spec was reconciled against.

## Common conventions

These conventions apply to every tool in this server and are therefore not repeated in each tool spec.

### Response transport

Tools return their output exclusively through MCP `content` blocks (`text`, `image`, etc.). The `structuredContent` field of the `CallToolResult` is never populated.

The upstream Claude Code CLI built-in tools return their output as unstructured content; emitting `structuredContent` in addition would diverge from that. The MCP specification also instructs clients to prefer `structuredContent` over `content` when both are present, so sending an empty `structuredContent` alongside the real `content` would cause those clients to display "{}" instead of the actual tool output.

Each tool spec's `Semantics` section describes which content block kinds it emits and any media-type-specific framing (for example, images returned as `image` blocks rather than as base64 in text).

## Client display limitations

These are limitations of common MCP clients rather than of this server. They are documented here so callers know the gap and can adjust their expectations.

### `Annotations.Audience` is not honoured

The MCP specification's `Annotations.Audience` field on tool-result content blocks (`["user"]`, `["assistant"]`, or both) would let a server split a long response into a user-visible summary and a model-visible full body. Claude Code CLI v2.1.x does not honour this hint: both content blocks are rendered in the TUI and both are forwarded to the model, regardless of `Annotations.Audience`.

Tracked upstream as [anthropics/claude-code#72239](https://github.com/anthropics/claude-code/issues/72239). Until the upstream client honours the hint, this server's tools render their full content into both the TUI and the model context, even on responses that would otherwise benefit from a per-block audience split.

## Refresh policy

The Claude Code CLI version pinned at the top of this README is the version whose published behaviour this spec attempts to mirror. When a new CLI release introduces a relevant change to a built-in tool, the spec is refreshed in two steps:

1. Bump the **Target** table and update **Sources** with the new snapshot pins.
2. Walk each tool file, mark any behaviour that has changed, and either align the implementation or record the divergence under **Known gaps** or **Known limitations**.

Spec refreshes happen in their own pull requests and are explicitly distinct from implementation pull requests, so the audit trail stays clean.
