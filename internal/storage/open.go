package storage

import (
	"context"
	"fmt"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
)

// Open constructs the storage backend selected by cfg.Backend. Backends are
// selected at runtime from configuration; switching backends requires only a
// configuration change, never a recompile.
func Open(ctx context.Context, cfg config.StorageConfig) (Store, error) {
	switch cfg.Backend {
	case config.StorageSQLite:
		return openSQLite(ctx, cfg)
	case config.StoragePostgres:
		return openPostgres(ctx, cfg)
	case config.StorageMySQL:
		return openMySQL(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
}
