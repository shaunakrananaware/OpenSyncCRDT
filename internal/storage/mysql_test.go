package storage

import (
	"context"
	"os"
	"testing"

	"github.com/opensynccrdt/opensynccrdt/internal/config"
)

// TestMySQLConformance runs the shared storage suite against a real MySQL
// instance. It is skipped unless TEST_MYSQL_URL is set (CI wires this to a
// service container), so local `go test ./...` stays hermetic.
func TestMySQLConformance(t *testing.T) {
	url := os.Getenv("TEST_MYSQL_URL")
	if url == "" {
		t.Skip("set TEST_MYSQL_URL to run MySQL conformance tests")
	}

	cfg := config.Default().Storage
	cfg.Backend = config.StorageMySQL
	cfg.URL = url

	store, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open mysql store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	my := store.(*mysqlStore)

	testStoreConformance(t, func(t *testing.T) Store {
		// Each subtest starts from an empty schema. Foreign-key checks are
		// disabled around the truncation so the parent table can be cleared
		// regardless of order; the shared store is reused across subtests.
		ctx := context.Background()
		if _, err := my.db.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0`); err != nil {
			t.Fatalf("disable fk checks: %v", err)
		}
		for _, tbl := range []string{"operations", "snapshots", "documents"} {
			if _, err := my.db.ExecContext(ctx, "TRUNCATE TABLE "+tbl); err != nil {
				t.Fatalf("truncate %s: %v", tbl, err)
			}
		}
		if _, err := my.db.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 1`); err != nil {
			t.Fatalf("enable fk checks: %v", err)
		}
		return my
	})
}
