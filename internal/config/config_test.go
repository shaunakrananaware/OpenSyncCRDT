package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Auth.Mode != AuthModeNone {
		t.Errorf("auth mode = %q, want none", cfg.Auth.Mode)
	}
	if cfg.Auth.HeaderName != "X-User-ID" {
		t.Errorf("header name = %q, want X-User-ID", cfg.Auth.HeaderName)
	}
	if cfg.Storage.Backend != StorageSQLite {
		t.Errorf("backend = %q, want sqlite", cfg.Storage.Backend)
	}
	if cfg.Storage.DataDir != "./data" {
		t.Errorf("data dir = %q, want ./data", cfg.Storage.DataDir)
	}
	if cfg.Storage.SnapshotInterval != 100 {
		t.Errorf("snapshot interval = %d, want 100", cfg.Storage.SnapshotInterval)
	}
	if cfg.Auth.WebhookTimeout != 3*time.Second {
		t.Errorf("auth webhook timeout = %v, want 3s", cfg.Auth.WebhookTimeout)
	}
	if cfg.Limits.MaxConnections != 10000 {
		t.Errorf("max connections = %d, want 10000", cfg.Limits.MaxConnections)
	}
	if cfg.Limits.MaxMessageSizeBytes != 1048576 {
		t.Errorf("max message size = %d, want 1048576", cfg.Limits.MaxMessageSizeBytes)
	}
	if !cfg.Management.Enabled {
		t.Errorf("management api should be enabled by default")
	}
	if len(cfg.CORS.AllowedOrigins) != 1 || cfg.CORS.AllowedOrigins[0] != "*" {
		t.Errorf("cors origins = %v, want [*]", cfg.CORS.AllowedOrigins)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9000")
	t.Setenv("AUTH_MODE", "header")
	t.Setenv("AUTH_HEADER_NAME", "X-Uid")
	t.Setenv("WEBHOOK_TIMEOUT", "10s")
	t.Setenv("STORAGE_SNAPSHOT_INTERVAL", "250")
	t.Setenv("DATA_DIR", "/srv/data")
	t.Setenv("MAX_CONNECTIONS", "500")
	t.Setenv("MAX_MESSAGE_SIZE_BYTES", "2097152")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com, https://b.com")
	t.Setenv("MANAGEMENT_API_ENABLED", "false")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("port = %d, want 9000", cfg.Server.Port)
	}
	if cfg.Auth.Mode != AuthModeHeader {
		t.Errorf("auth mode = %q, want header", cfg.Auth.Mode)
	}
	if cfg.Auth.HeaderName != "X-Uid" {
		t.Errorf("header name = %q, want X-Uid", cfg.Auth.HeaderName)
	}
	if cfg.Webhooks.Timeout != 10*time.Second {
		t.Errorf("webhook timeout = %v, want 10s", cfg.Webhooks.Timeout)
	}
	if cfg.Storage.SnapshotInterval != 250 {
		t.Errorf("snapshot interval = %d, want 250", cfg.Storage.SnapshotInterval)
	}
	if cfg.Storage.DataDir != "/srv/data" {
		t.Errorf("data dir = %q, want /srv/data", cfg.Storage.DataDir)
	}
	if cfg.Limits.MaxConnections != 500 {
		t.Errorf("max connections = %d, want 500", cfg.Limits.MaxConnections)
	}
	if cfg.Limits.MaxMessageSizeBytes != 2097152 {
		t.Errorf("max message size = %d, want 2097152", cfg.Limits.MaxMessageSizeBytes)
	}
	if len(cfg.CORS.AllowedOrigins) != 2 || cfg.CORS.AllowedOrigins[0] != "https://a.com" {
		t.Errorf("cors origins = %v", cfg.CORS.AllowedOrigins)
	}
	if cfg.Management.Enabled {
		t.Errorf("management api should be disabled by env")
	}
}

func TestWebhookEventEnvMapping(t *testing.T) {
	clearEnv(t)
	t.Setenv("WEBHOOK_ON_DOCUMENT_CREATED_URL", "https://app/created")
	t.Setenv("WEBHOOK_ON_DOCUMENT_UPDATED_URL", "https://app/updated")
	// Reserved keys must not be treated as events.
	t.Setenv("WEBHOOK_SECRET", "s3cr3t")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Webhooks.Events["on_document_created"]; got != "https://app/created" {
		t.Errorf("created url = %q", got)
	}
	if got := cfg.Webhooks.Events["on_document_updated"]; got != "https://app/updated" {
		t.Errorf("updated url = %q", got)
	}
	if _, ok := cfg.Webhooks.Events["secret"]; ok {
		t.Errorf("SECRET was wrongly parsed as an event")
	}
	if cfg.Webhooks.Secret != "s3cr3t" {
		t.Errorf("webhook secret = %q", cfg.Webhooks.Secret)
	}
}

func TestConfigFileThenEnvPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  port: 7777
auth:
  mode: webhook
  webhook_url: https://example.com/verify
storage:
  data_dir: /data/custom
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Env should win over the file value for port.
	t.Setenv("PORT", "8888")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != 8888 {
		t.Errorf("port = %d, want 8888 (env wins over file)", cfg.Server.Port)
	}
	if cfg.Auth.Mode != AuthModeWebhook {
		t.Errorf("auth mode = %q, want webhook (from file)", cfg.Auth.Mode)
	}
	if cfg.Auth.WebhookURL != "https://example.com/verify" {
		t.Errorf("webhook url = %q", cfg.Auth.WebhookURL)
	}
	if cfg.Storage.DataDir != "/data/custom" {
		t.Errorf("data dir = %q", cfg.Storage.DataDir)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"bad port", func(c *Config) { c.Server.Port = 70000 }},
		{"webhook mode without url", func(c *Config) { c.Auth.Mode = AuthModeWebhook }},
		{"unknown backend", func(c *Config) { c.Storage.Backend = "cassandra" }},
		{"postgres without url", func(c *Config) { c.Storage.Backend = StoragePostgres }},
		{"bad log level", func(c *Config) { c.Log.Level = "loud" }},
		{"zero snapshot interval", func(c *Config) { c.Storage.SnapshotInterval = 0 }},
		{"tls without files", func(c *Config) { c.TLS.Enabled = true }},
		{"cluster with sqlite", func(c *Config) { c.Cluster.Mode = true }},
		{"cluster without redis url", func(c *Config) {
			c.Cluster.Mode = true
			c.Storage.Backend = StoragePostgres
			c.Storage.URL = "postgres://u:p@h/db"
		}},
		{"cluster with unsupported backend", func(c *Config) {
			c.Cluster.Mode = true
			c.Cluster.Backend = "nats"
			c.Storage.Backend = StoragePostgres
			c.Storage.URL = "postgres://u:p@h/db"
			c.Cluster.RedisURL = "redis://localhost:6379"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestClusterWithPostgresIsValid(t *testing.T) {
	cfg := Default()
	cfg.Cluster.Mode = true
	cfg.Storage.Backend = StoragePostgres
	cfg.Storage.URL = "postgres://u:p@h/db"
	cfg.Cluster.RedisURL = "redis://localhost:6379"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cluster+postgres should be valid: %v", err)
	}
}

func TestInvalidEnvValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(""); err == nil {
		t.Error("expected error for non-numeric PORT")
	}
	os.Unsetenv("PORT")

	t.Setenv("CLUSTER_MODE", "maybe")
	if _, err := Load(""); err == nil {
		t.Error("expected error for non-boolean CLUSTER_MODE")
	}
}

func TestUnknownConfigFileKeyRejected(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  porttt: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected error for unknown config key")
	}
}

// clearEnv removes every environment variable this package reads so a test
// starts from a clean slate regardless of the host environment.
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"HOST", "PORT",
		"TLS_ENABLED", "TLS_CERT_FILE", "TLS_KEY_FILE",
		"STORAGE_BACKEND", "DATA_DIR", "STORAGE_URL", "STORAGE_SNAPSHOT_INTERVAL",
		"STORAGE_POSTGRES_MAX_CONNS", "STORAGE_POSTGRES_SSL_MODE", "STORAGE_MYSQL_MAX_CONNS",
		"AUTH_MODE", "AUTH_HEADER_NAME", "AUTH_WEBHOOK_URL", "AUTH_WEBHOOK_SECRET",
		"AUTH_WEBHOOK_TIMEOUT", "AUTH_WEBHOOK_CACHE_TTL",
		"CONFLICT_RESOLVER_URL", "CONFLICT_RESOLVER_SECRET", "CONFLICT_RESOLVER_TIMEOUT",
		"WEBHOOK_SECRET", "WEBHOOK_TIMEOUT", "WEBHOOK_MAX_RETRIES",
		"WEBHOOK_ON_DOCUMENT_CREATED_URL", "WEBHOOK_ON_DOCUMENT_UPDATED_URL",
		"WEBHOOK_ON_DOCUMENT_DELETED_URL", "WEBHOOK_ON_CLIENT_CONNECTED_URL",
		"WEBHOOK_ON_CLIENT_DISCONNECTED_URL", "WEBHOOK_ON_SYNC_ERROR_URL",
		"CLUSTER_MODE", "CLUSTER_BACKEND", "CLUSTER_REDIS_URL",
		"MAX_CONNECTIONS", "MAX_MESSAGE_SIZE_BYTES", "PING_INTERVAL", "PONG_TIMEOUT",
		"WRITE_TIMEOUT", "READ_TIMEOUT",
		"CORS_ALLOWED_ORIGINS", "MANAGEMENT_API_ENABLED", "MANAGEMENT_API_KEY",
		"LOG_LEVEL", "LOG_FORMAT",
	}
	for _, k := range keys {
		if _, ok := os.LookupEnv(k); ok {
			t.Setenv(k, "") // register cleanup
			os.Unsetenv(k)
		}
	}
}
