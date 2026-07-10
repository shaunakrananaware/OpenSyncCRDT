package storage

import (
	"context"
	"os"
	"testing"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
)

// TestPostgresConformance runs the shared storage suite against a real
// PostgreSQL instance. It is skipped unless TEST_POSTGRES_URL is set (CI wires
// this to a service container), so local `go test ./...` stays hermetic.
func TestPostgresConformance(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("set TEST_POSTGRES_URL to run Postgres conformance tests")
	}

	cfg := config.Default().Storage
	cfg.Backend = config.StoragePostgres
	cfg.URL = url

	store, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	pg := store.(*postgresStore)

	testStoreConformance(t, func(t *testing.T) Store {
		// Each subtest starts from an empty schema. CASCADE clears the child
		// tables; the shared store is reused across subtests.
		if _, err := pg.pool.Exec(context.Background(),
			`TRUNCATE documents, operations, snapshots RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return pg
	})
}
