# Security Policy

## Supported versions

Security fixes are issued for the latest minor release. Older minor releases
may receive patches at the maintainers' discretion.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

## Reporting a vulnerability

Please report security vulnerabilities through GitHub's
[Private Vulnerability Reporting](https://github.com/delight-co/claude-code-fs-shell-mcp/security/advisories/new).
Do **not** open a public issue.

We aim to acknowledge new reports within 5 business days and to ship a fix
or mitigation within 90 days. Coordinated disclosure timelines are agreed
with the reporter on a case-by-case basis.

## Scope

In scope:

- The `claude-code-fs-shell-mcp` daemon and its published container image.
- Build, release, and signing pipelines defined in this repository.

Out of scope:

- Sandbox / container / VM enforcement. The daemon is intentionally
  sandbox-agnostic: isolation is the responsibility of the hosting
  environment, not of this project.
- Vulnerabilities in third-party dependencies, unless they have a
  meaningful impact on this project's documented behaviour. Please
  report upstream first.
