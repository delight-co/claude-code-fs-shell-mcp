# syntax=docker/dockerfile:1.7
#
# Reference image for trying out claude-code-fs-shell-mcp.
#
# This image ships the daemon and nothing else. It does not provide a sandbox,
# credential broker, or any other isolation primitive: those are the
# responsibility of whatever environment installs this binary (Docker compose,
# Kubernetes, Firecracker, gVisor, plain SSH onto a VM - your choice).
#
# The image is consumed by GoReleaser's dockers_v2 pipeline. The
# `--from=context` mount exposes the cross-compiled artifact GoReleaser
# stages under ${TARGETPLATFORM}/.

FROM gcr.io/distroless/static-debian13:nonroot

ARG TARGETPLATFORM

COPY --from=context ${TARGETPLATFORM}/claude-code-fs-shell-mcp /usr/local/bin/claude-code-fs-shell-mcp

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/claude-code-fs-shell-mcp"]
