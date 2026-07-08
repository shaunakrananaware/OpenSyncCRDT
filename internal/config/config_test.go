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
	if cfg.Auth.WebhookTimeout != 3*time.Second {
		t.Errorf("auth webhook timeout = %v, want 3s", cfg.Auth.WebhookTimeout)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9000")
	t.Setenv("AUTH_MODE", "header")
	t.Setenv("AUTH_HEADER_NAME", "X-Uid")
	t.Setenv("WEBHOOK_TIMEOUT", "10s")
	t.Setenv("SNAPSHOT_INTERVAL_OPS", "250")

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
	if cfg.Snapshot.IntervalOps != 250 {
		t.Errorf("snapshot interval = %d, want 250", cfg.Snapshot.IntervalOps)
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
  sqlite_path: /data/custom.db
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
	if cfg.Storage.SQLitePath != "/data/custom.db" {
		t.Errorf("sqlite path = %q", cfg.Storage.SQLitePath)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"bad port", func(c *Config) { c.Server.Port = 70000 }},
		{"webhook mode without url", func(c *Config) { c.Auth.Mode = AuthModeWebhook }},
		{"unknown backend", func(c *Config) { c.Storage.Backend = "postgres" }},
		{"bad log level", func(c *Config) { c.Log.Level = "loud" }},
		{"zero snapshot interval", func(c *Config) { c.Snapshot.IntervalOps = 0 }},
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

func TestInvalidEnvValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(""); err == nil {
		t.Error("expected error for non-numeric PORT")
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
		"CONFIG_FILE", "HOST", "PORT", "SERVER_SHUTDOWN_TIMEOUT",
		"STORAGE_BACKEND", "SQLITE_PATH", "SQLITE_BUSY_TIMEOUT", "STORAGE_MAX_OPEN_CONNS",
		"AUTH_MODE", "AUTH_HEADER_NAME", "AUTH_WEBHOOK_URL", "AUTH_WEBHOOK_SECRET",
		"AUTH_WEBHOOK_TIMEOUT", "AUTH_WEBHOOK_CACHE_TTL",
		"CONFLICT_RESOLVER_URL", "CONFLICT_RESOLVER_SECRET", "CONFLICT_RESOLVER_TIMEOUT",
		"WEBHOOK_SECRET", "WEBHOOK_TIMEOUT", "WEBHOOK_MAX_RETRIES",
		"WEBHOOK_ON_DOCUMENT_CREATED_URL", "WEBHOOK_ON_DOCUMENT_UPDATED_URL",
		"SNAPSHOT_INTERVAL_OPS", "LOG_LEVEL", "LOG_FORMAT",
	}
	for _, k := range keys {
		if _, ok := os.LookupEnv(k); ok {
			t.Setenv(k, "") // register cleanup
			os.Unsetenv(k)
		}
	}
}
