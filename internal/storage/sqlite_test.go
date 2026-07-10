package storage

import (
	"context"
	"testing"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
)

// newSQLiteTestStore opens a fresh SQLite store in a per-test temp directory.
// A new directory per invocation gives each conformance subtest an isolated,
// empty database.
func newSQLiteTestStore(t *testing.T) Store {
	t.Helper()
	cfg := config.Default().Storage
	cfg.DataDir = t.TempDir()
	cfg.MaxOpenConns = 4 // exercise concurrency
	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLiteConformance(t *testing.T) {
	testStoreConformance(t, newSQLiteTestStore)
}
