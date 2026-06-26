// Package server constructs the MCP server and the HTTP handler used by the
// claude-code-fs-shell-mcp binary.
package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/delight-co/claude-code-fs-shell-mcp/internal/tools"
)

// implementationName is the name reported in the MCP initialise handshake.
const implementationName = "claude-code-fs-shell-mcp"

// Version is the implementation version reported in the MCP handshake.
// The default is overridden at build time via -ldflags by GoReleaser.
var Version = "0.0.0-dev"

// New returns an HTTP handler that serves Streamable HTTP MCP requests.
//
// The handler operates in stateful mode: the SDK validates the
// Mcp-Session-Id header on every request, which lets tools attach
// per-session state. The Write tool family needs this for the
// read-before-overwrite contract; the supplied registry holds that
// state on behalf of all tools.
func New(logger *slog.Logger, registry *ReadTrackingRegistry) (http.Handler, error) {
	if logger == nil {
		return nil, errors.New("server.New: logger must not be nil")
	}
	if registry == nil {
		return nil, errors.New("server.New: registry must not be nil")
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    implementationName,
		Version: Version,
	}, nil)

	tools.RegisterRead(mcpServer, tools.DefaultReadConfig(), logger, registry)
	tools.RegisterWrite(mcpServer, tools.DefaultWriteConfig(), logger, registry)

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{Stateless: false},
	)
	if handler == nil {
		return nil, errors.New("server.New: mcp.NewStreamableHTTPHandler returned nil")
	}

	logger.Info("mcp server initialised",
		"implementation", implementationName,
		"version", Version,
		"transport", "streamable-http",
		"stateless", false,
	)
	return withRequestLog(handler, logger), nil
}
