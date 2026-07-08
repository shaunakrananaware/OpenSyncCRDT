// Command opensynccrdt is the single-binary OpenSyncCRDT sync engine.
//
// At this stage it wires up the foundation — configuration, logging, and the
// storage layer — and runs a graceful lifecycle. The transport and sync layers
// are added on top of this foundation in later work.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
	"github.com/opensynccrdt/opensynccrdt/internal/storage"
	"github.com/opensynccrdt/opensynccrdt/internal/storage/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to YAML config file (overrides CONFIG_FILE env)")
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

	store, err := openStore(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			logger.Error("closing storage", "error", cerr)
		}
	}()

	logger.Info("OpenSyncCRDT foundation ready",
		"addr", cfg.Addr(),
		"storage_backend", cfg.Storage.Backend,
		"auth_mode", cfg.Auth.Mode,
		"log_level", cfg.Log.Level,
	)

	// The transport/sync layers will block here in later work. For now the
	// process stays up until a shutdown signal so operators can validate the
	// foundation end-to-end.
	<-ctx.Done()
	logger.Info("shutdown signal received, exiting")
	return nil
}

// openStore selects and opens the configured storage backend. Backend
// selection lives here (rather than in the storage package) so the storage
// contract package need not import concrete backends.
func openStore(ctx context.Context, cfg config.StorageConfig) (storage.Store, error) {
	switch cfg.Backend {
	case config.StorageSQLite:
		return sqlite.Open(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
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
