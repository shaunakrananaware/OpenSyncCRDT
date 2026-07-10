// Command opensynccrdt is the single-binary OpenSyncCRDT sync engine: a
// local-first sync server that a developer can run with one binary or one
// Docker command. It loads configuration, assembles the engine, and runs a
// graceful HTTP/WebSocket lifecycle.
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
	"strings"
	"syscall"

	"github.com/shaunakrananaware/OpenSyncCRDT/internal/config"
	"github.com/shaunakrananaware/OpenSyncCRDT/pkg/engine"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)

	// Signals drive graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eng, err := engine.New(engine.Config{Full: &cfg})
	if err != nil {
		return err
	}
	defer eng.Close()

	httpServer := &http.Server{
		Addr:    cfg.Addr(),
		Handler: eng.Handler(),
	}
	if cfg.Limits.ReadTimeout > 0 {
		httpServer.ReadTimeout = cfg.Limits.ReadTimeout
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("OpenSyncCRDT listening",
			"version", version,
			"addr", cfg.Addr(),
			"storage_backend", cfg.Storage.Backend,
			"auth_mode", cfg.Auth.Mode,
			"tls", cfg.TLS.Enabled,
			"log_level", cfg.Log.Level,
		)
		var serr error
		if cfg.TLS.Enabled {
			serr = httpServer.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		} else {
			serr = httpServer.ListenAndServe()
		}
		if serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			serveErr <- serr
		}
		close(serveErr)
	}()

	select {
	case serr := <-serveErr:
		if serr != nil {
			return fmt.Errorf("http server: %w", serr)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections")
	}

	stop() // a second Ctrl-C now hard-exits
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown timed out", "error", err)
		_ = httpServer.Close()
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}
