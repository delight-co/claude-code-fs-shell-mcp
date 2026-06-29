// Command claude-code-fs-shell-mcp serves filesystem and shell tools over the
// Model Context Protocol using the Streamable HTTP transport.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/delight-co/claude-code-fs-shell-mcp/internal/server"
	"github.com/delight-co/claude-code-fs-shell-mcp/internal/tools"
)

const (
	defaultAddr                   = "127.0.0.1:8080"
	defaultReadHeaderTimeout      = 10 * time.Second
	defaultIdleTimeout            = 120 * time.Second
	defaultShutdownTimeout        = 10 * time.Second
	defaultReadTrackingMaxEntries = 256
	defaultSessionTTL             = 60 * time.Second
)

func main() {
	addr := flag.String("addr", envOr("CCFS_MCP_ADDR", defaultAddr), "address to listen on")
	logFormat := flag.String("log-format", envOr("CCFS_MCP_LOG_FORMAT", "json"), "log format: json or text")
	flag.Parse()

	logger := newLogger(*logFormat)
	slog.SetDefault(logger)

	if err := run(*addr, logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(addr string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	maxEntries := envIntOr("CCFS_MCP_READ_TRACKING_MAX_ENTRIES", defaultReadTrackingMaxEntries)
	sessionTTL := envDurationSecondsOr("CCFS_MCP_SESSION_TTL_SECONDS", defaultSessionTTL)
	registry := server.NewReadTrackingRegistry(maxEntries, sessionTTL, logger)
	logger.Info("read tracking registry initialised",
		"max_entries_per_session", maxEntries,
		"session_ttl_seconds", int(sessionTTL.Seconds()),
	)

	rgPath, rgCleanup, rgErr := tools.ResolveRipgrep()
	if rgErr != nil {
		logger.Warn("ripgrep resolution failed; grep and glob will return ENOENT until the host provides rg", "err", rgErr)
	} else {
		logger.Info("ripgrep resolved", "path", rgPath)
	}
	defer rgCleanup()

	handler, err := server.New(logger, registry, rgPath)
	if err != nil {
		return fmt.Errorf("build mcp handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		IdleTimeout:       defaultIdleTimeout,
	}

	logger.Info("starting server",
		"addr", addr,
		"version", buildVersion(),
	)

	errCh := make(chan error, 1)
	go func() {
		if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			errCh <- listenErr
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received", "timeout", defaultShutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			return fmt.Errorf("graceful shutdown: %w", shutdownErr)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func envDurationSecondsOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return fallback
}

func newLogger(format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	default:
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func buildVersion() string {
	if v := server.Version; v != "" && v != "0.0.0-dev" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value
		}
	}
	return "(devel)"
}
