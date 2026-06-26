# AGENTS.md

Guidance for AI coding agents (Claude Code, Cursor, OpenAI Codex CLI, etc.) working in this repository.

## Project facts

- **Apache-2.0 licensed open-source project**, published at https://github.com/delight-co/claude-code-fs-shell-mcp.
- A Model Context Protocol (MCP) server that exposes filesystem and shell tools mirroring the built-in `Read`, `Write`, `Edit`, `Bash`, `Grep`, and `Glob` tools shipped with the Claude Code CLI.
- Goal: behavioural parity with those built-in tools so an agent can route its filesystem and shell traffic through this server without changing how it thinks about its tools.
- Not affiliated with, endorsed by, or sponsored by Anthropic.

## All written artifacts MUST be in English

Code, comments, commit messages, pull request titles and bodies, issue titles and bodies, review comments — everything that ends up in the repository or on its GitHub surface — is written in English. No other language slips into public artifacts.

## Do not leak organization-internal information

Treat the public GitHub surface (repository contents, commits, pull request and issue threads, release notes, GitHub Actions logs, container images) as world-readable.

Never write to those surfaces:

- Internal repository names, paths, project codenames, or chat-thread excerpts.
- Names of contributors who have not opted in publicly via `CODEOWNERS` or a public commit.
- Links to private resources.
- Internal discussions about strategy, costs, vendors, or roadmaps that are not already public.
- Any content that would not survive scrutiny if a competitor, a security researcher, or a hostile party read it.

If unsure, ask before publishing.

## Specs are the source of truth

The specifications under `docs/spec/` describe the behaviour this server must implement to remain compatible with the upstream Claude Code CLI built-in tools. Implementations satisfy specs, not the other way around.

- When changing behaviour, update the spec first (in the same pull request as the implementation, or in a separate spec-refresh pull request).
- When the upstream CLI releases a new version that changes a built-in tool's behaviour, refresh the spec (see `docs/spec/README.md#refresh-policy`) before adjusting the implementation.
- See `docs/spec/README.md#common-conventions` for cross-cutting rules that apply to every tool.

## Local development checks before pushing

Run all of these locally and make sure they pass before opening a pull request:

```sh
go build ./...
go test ./...
golangci-lint run ./...
```

The pre-commit hooks managed by [lefthook](https://github.com/evilmartians/lefthook) wire these into your git workflow. Install them once with:

```sh
lefthook install
```

CI runs the same checks (plus security scans like `govulncheck` and `gosec`). If CI fails, the underlying problem is reproducible locally with the commands above.

For end-to-end verification with a running Claude Code CLI session, see [CONTRIBUTING.md](./CONTRIBUTING.md#verifying-changes-against-a-running-claude-code-cli).

## Pull request and issue conventions

- Follow the templates under `.github/PULL_REQUEST_TEMPLATE.md` and `.github/ISSUE_TEMPLATE/`.
- Reference related issues (`Fixes #N`, `Refs #N`) where applicable.
- Keep pull requests scoped: one logical change per PR.

## Reporting security issues

See `SECURITY.md`. Vulnerabilities are reported privately via GitHub Private Vulnerability Reporting.
