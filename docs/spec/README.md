# Specification

This directory captures the specification that this MCP server aims to be compatible with: the **filesystem and shell tools built into the Claude Code CLI**. It exists because the value of this project rests on a single claim — that an agent can swap our MCP tools in for the built-ins and behave the same way — and that claim needs to be auditable, not implicit.

The Claude Code CLI is stable but does change occasionally. To make our compatibility window observable, we pin a target version on every spec refresh and bump it deliberately.

## Target

| Field | Value |
| ----- | ----- |
| Claude Code CLI version we target | `v2.1.191` (released 2026-06-24) |
| Spec last refreshed | 2026-06-25 |
| Tools covered | `Read`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `Grep`, `Glob` |

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
| [Read](./read.md)         | 🟢 | 🔴 | Image returned as MCP `image` content; PDF transport details flagged for observation. |
| [Write](./write.md)       | 🟡 | 🔴 | Read-before-overwrite semantics is the central rule. |
| [Edit](./edit.md)         | 🟡 | 🔴 | Three pre-checks (read-before-edit, exact match, uniqueness) are non-negotiable. |
| [NotebookEdit](./notebookedit.md) | 🟡 | ❌ | Deferred. Out of scope for the initial milestones; spec retained so the gap is visible. |
| [Bash](./bash.md)         | 🟡 | 🔴 | Working-directory persistence and output-truncation behaviour are the largest design questions. |
| [Grep](./grep.md)         | 🟡 | 🔴 | Thin wrapper around ripgrep. |
| [Glob](./glob.md)         | 🟡 | 🔴 | Modification-time sort and a result cap are part of the contract. |

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

## Refresh policy

The Claude Code CLI version pinned at the top of this README is the version whose published behaviour this spec attempts to mirror. When a new CLI release introduces a relevant change to a built-in tool, the spec is refreshed in two steps:

1. Bump the **Target** table and update **Sources** with the new snapshot pins.
2. Walk each tool file, mark any behaviour that has changed, and either align the implementation or record the divergence under **Known gaps** or **Known limitations**.

Spec refreshes happen in their own pull requests and are explicitly distinct from implementation pull requests, so the audit trail stays clean.
