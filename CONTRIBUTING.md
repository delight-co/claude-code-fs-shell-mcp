# Contributing

Thanks for your interest. A few practical notes before you open a pull
request.

## Code of Conduct

This project follows the [Contributor Covenant](./CODE_OF_CONDUCT.md). By
participating, you agree to uphold it.

## Local development

Requirements:

- Go 1.25 or 1.26 (the `toolchain` directive in `go.mod` pulls the exact
  patch version automatically).
- [`golangci-lint`](https://golangci-lint.run/) v2.12.x.
- [`lefthook`](https://github.com/evilmartians/lefthook) (optional, for the
  pre-commit hooks).

```sh
go mod download
go build ./...
go test -race ./...
golangci-lint run
golangci-lint fmt
```

Install the git hooks once with:

```sh
lefthook install
```

## Verifying changes against a running Claude Code CLI

The unit and integration tests under `internal/` cover the server in isolation. To verify a change end-to-end through a real MCP client, run the server locally and register it with the Claude Code CLI:

```sh
# 1. Start the server.
go run ./cmd/claude-code-fs-shell-mcp --addr 127.0.0.1:8080

# 2. In another shell, register the server with the CLI under a short alias.
claude mcp add ccfs --transport http http://127.0.0.1:8080/mcp

# 3. Start a Claude Code CLI session in the directory you want to exercise.
#    The server's tools will appear as `mcp__ccfs__read` (and `mcp__ccfs__write`,
#    `mcp__ccfs__bash`, etc., as more tools land). Ask the agent to use them
#    on real files and observe the responses.

# 4. When you are done, remove the registration and stop the server.
claude mcp remove ccfs
# (then Ctrl+C on the server process)
```

The short alias `ccfs` is suggested so that tool names appear as `mcp__ccfs__read` rather than `mcp__claude-code-fs-shell-mcp__read`. Use whatever alias makes sense for your setup.

Server-side request logging is intentionally minimal. When you need a richer view of what the server is doing, add temporary `slog` calls and recompile.

## Pull requests

- Keep diffs focused. One conceptual change per pull request.
- Prefer [Conventional Commits](https://www.conventionalcommits.org/) for
  the pull request title. It is not strictly enforced on individual commit
  messages but it does drive the release notes.
- All CI jobs must pass before merge.

## Reporting bugs and proposing features

Use the issue templates under `.github/ISSUE_TEMPLATE/`.

## Security

Do not file public issues for security problems. See [SECURITY.md](./SECURITY.md).
