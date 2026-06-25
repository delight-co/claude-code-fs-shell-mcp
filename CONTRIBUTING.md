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
