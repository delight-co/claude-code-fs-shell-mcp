// Package server constructs the MCP server and the HTTP handler used by the
// claude-code-fs-shell-mcp binary.
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// implementationName is the name reported in the MCP initialise handshake.
const implementationName = "claude-code-fs-shell-mcp"

// Version is the implementation version reported in the MCP handshake.
// The default is overridden at build time via -ldflags by GoReleaser.
var Version = "0.0.0-dev"

// New returns an HTTP handler that serves Streamable HTTP MCP requests.
//
// The handler operates in stateless mode: each HTTP request is treated as an
// independent session. Stateless mode trades server-initiated requests
// (sampling, elicitation, progress) for horizontal scalability and
// load-balancer friendliness.
func New(logger *slog.Logger) (http.Handler, error) {
	if logger == nil {
		return nil, fmt.Errorf("server.New: logger must not be nil")
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    implementationName,
		Version: Version,
	}, nil)

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	if handler == nil {
		return nil, fmt.Errorf("server.New: mcp.NewStreamableHTTPHandler returned nil")
	}

	logger.Info("mcp server initialised",
		"implementation", implementationName,
		"version", Version,
		"transport", "streamable-http",
		"stateless", true,
	)
	return handler, nil
}
