# claude-code-fs-shell-mcp

Filesystem and shell tools for AI coding agents, exposed over the
[Model Context Protocol](https://modelcontextprotocol.io/) Streamable HTTP
transport.

The tool surface mirrors the built-in `Read`, `Write`, `Edit`, `Bash`,
`Grep`, and `Glob` tools shipped with the
[Claude Code](https://docs.claude.com/en/docs/claude-code/) CLI, so you can
route an agent's filesystem and shell traffic through an MCP server without
changing how the agent thinks about its tools.

> **Project status:** scaffolding. The transport, configuration, and release
> pipeline are wired up. The actual tool implementations land in follow-up
> commits.

## Why

If you want to run a coding agent inside an isolation primitive of your
choice (a Docker container, a Firecracker microVM, a remote VM, ...) and
have its `Read` / `Write` / `Edit` / `Bash` / `Grep` / `Glob` operations
execute inside that primitive rather than on the agent's host, you need an
MCP server that:

1. speaks the same tool shapes the agent already knows, and
2. installs cleanly into whatever isolation primitive you picked.

`claude-code-fs-shell-mcp` is that server. It is intentionally
**sandbox-agnostic**: the binary does not provide isolation, credential
brokering, or a network policy of its own. Those decisions belong to the
environment that hosts the server.

## Install

### Pre-built binary

Download an archive from the
[Releases page](https://github.com/delight-co/claude-code-fs-shell-mcp/releases),
verify the cosign bundle (`*.sigstore.json`), and extract the binary. See
[Verify a release](#verify-a-release).

### Container image

```sh
docker pull ghcr.io/delight-co/claude-code-fs-shell-mcp:latest
docker run --rm -p 8080:8080 \
  ghcr.io/delight-co/claude-code-fs-shell-mcp:latest --addr 0.0.0.0:8080
```

The image is based on
[`gcr.io/distroless/static-debian13:nonroot`](https://github.com/GoogleContainerTools/distroless)
and contains only the daemon. Treat it as a reference image; production
deployments should compose it (or the raw binary) into whatever sandbox
they prefer.

### From source

```sh
go install github.com/delight-co/claude-code-fs-shell-mcp/cmd/claude-code-fs-shell-mcp@latest
```

## Run

```sh
claude-code-fs-shell-mcp --addr 127.0.0.1:8080
```

The server speaks MCP Streamable HTTP at `/mcp` and exposes `/healthz` for
liveness probes. It runs in stateless mode: every request is treated as an
independent session, which makes the server load-balancer friendly but
disables server-initiated requests (sampling, elicitation, progress).

### Configuration

| Flag           | Environment variable    | Default          |
| -------------- | ----------------------- | ---------------- |
| `--addr`       | `CCFS_MCP_ADDR`         | `127.0.0.1:8080` |
| `--log-format` | `CCFS_MCP_LOG_FORMAT`   | `json`           |

`--log-format` accepts `json` (production) or `text` (human-readable).

## Verify a release

Release archives are signed keylessly with
[cosign](https://github.com/sigstore/cosign) using GitHub Actions OIDC.
Each release also publishes a build provenance attestation through
[GitHub Artifact Attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations).

```sh
# Verify the signature on the checksums file
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/delight-co/claude-code-fs-shell-mcp/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --bundle checksums.txt.sigstore.json \
  checksums.txt

# Verify the build provenance attestation
gh attestation verify checksums.txt --owner delight-co
```

Container images are signed with `cosign sign` and can be verified with:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/delight-co/claude-code-fs-shell-mcp/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/delight-co/claude-code-fs-shell-mcp:latest
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Bug reports and security
advisories go through
[Private Vulnerability Reporting](./SECURITY.md).

This project follows the [Contributor Covenant](./CODE_OF_CONDUCT.md).

## License

[Apache-2.0](./LICENSE).

---

This project is independent and is not affiliated with, endorsed by, or
sponsored by Anthropic. _Claude_ and _Claude Code_ are trademarks of
Anthropic, PBC.
