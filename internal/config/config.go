// Package config defines the complete runtime configuration for OpenSyncCRDT.
//
// Every behaviour that can be configured is configurable via environment
// variables or an optional YAML config file — never via code changes.
//
// Precedence, lowest to highest:
//
//	built-in defaults  <  config file  <  environment variables
//
// So an operator can ship a config file with a deployment and still override
// any single value at runtime with an environment variable.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AuthMode selects how incoming connections are authenticated. See the auth
// section of the specification for the full semantics of each mode.
type AuthMode string

const (
	// AuthModeNone accepts all connections. Default.
	AuthModeNone AuthMode = "none"
	// AuthModeHeader trusts an upstream-injected identity header.
	AuthModeHeader AuthMode = "header"
	// AuthModeWebhook calls the developer's endpoint on every new connection.
	AuthModeWebhook AuthMode = "webhook"
)

// StorageBackend selects the persistence engine.
type StorageBackend string

const (
	// StorageSQLite is the embedded single-file SQLite backend. Default.
	StorageSQLite StorageBackend = "sqlite"
)

// Config is the fully-resolved configuration for a running instance.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Auth     AuthConfig     `yaml:"auth"`
	Conflict ConflictConfig `yaml:"conflict"`
	Webhooks WebhookConfig  `yaml:"webhooks"`
	Snapshot SnapshotConfig `yaml:"snapshot"`
	Log      LogConfig      `yaml:"log"`
}

// ServerConfig controls the HTTP/WebSocket listener.
type ServerConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// StorageConfig controls the persistence layer.
type StorageConfig struct {
	Backend     StorageBackend `yaml:"backend"`
	SQLitePath  string         `yaml:"sqlite_path"`
	BusyTimeout time.Duration  `yaml:"busy_timeout"`
	// MaxOpenConns caps concurrent connections to the database. SQLite writers
	// are serialized, so a small pool is usually correct.
	MaxOpenConns int `yaml:"max_open_conns"`
}

// AuthConfig controls connection authentication.
type AuthConfig struct {
	Mode            AuthMode      `yaml:"mode"`
	HeaderName      string        `yaml:"header_name"`
	WebhookURL      string        `yaml:"webhook_url"`
	WebhookSecret   string        `yaml:"webhook_secret"`
	WebhookTimeout  time.Duration `yaml:"webhook_timeout"`
	WebhookCacheTTL time.Duration `yaml:"webhook_cache_ttl"`
}

// ConflictConfig controls the optional custom conflict-resolver webhook. When
// ResolverURL is empty, Automerge resolves conflicts automatically.
type ConflictConfig struct {
	ResolverURL    string        `yaml:"resolver_url"`
	ResolverSecret string        `yaml:"resolver_secret"`
	Timeout        time.Duration `yaml:"timeout"`
}

// WebhookConfig controls outbound event webhooks. Events maps an event name
// (e.g. "on_document_created") to the URL that should receive it. An unset or
// empty URL means the event is silently skipped.
type WebhookConfig struct {
	Secret     string            `yaml:"secret"`
	Timeout    time.Duration     `yaml:"timeout"`
	MaxRetries int               `yaml:"max_retries"`
	Events     map[string]string `yaml:"events"`
}

// SnapshotConfig controls how often a full Automerge state snapshot is written
// so that startup does not have to replay the entire op log.
type SnapshotConfig struct {
	// IntervalOps triggers a snapshot every N committed operations on a doc.
	IntervalOps int `yaml:"interval_ops"`
}

// LogConfig controls logging.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json or text
}

// Default returns a Config populated with the specification's default values.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
		},
		Storage: StorageConfig{
			Backend:      StorageSQLite,
			SQLitePath:   "./opensynccrdt.db",
			BusyTimeout:  5 * time.Second,
			MaxOpenConns: 1,
		},
		Auth: AuthConfig{
			Mode:            AuthModeNone,
			HeaderName:      "X-User-ID",
			WebhookTimeout:  3 * time.Second,
			WebhookCacheTTL: 60 * time.Second,
		},
		Conflict: ConflictConfig{
			Timeout: 5 * time.Second,
		},
		Webhooks: WebhookConfig{
			Timeout:    5 * time.Second,
			MaxRetries: 3,
			Events:     map[string]string{},
		},
		Snapshot: SnapshotConfig{
			IntervalOps: 100,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load resolves configuration from (in increasing precedence) defaults, an
// optional YAML config file, and environment variables. The config file path
// is taken from the CONFIG_FILE environment variable when path is empty. A
// missing config file is not an error unless it was explicitly requested.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		path = os.Getenv("CONFIG_FILE")
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config file %q: %w", path, err)
		}
		// KnownFields guards against typos in the config file rather than
		// silently ignoring them.
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyEnv overlays environment variables onto cfg. Only variables that are
// actually set take effect, preserving lower-precedence values otherwise.
func applyEnv(cfg *Config) error {
	var errs []string
	errf := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	envStr("HOST", &cfg.Server.Host)
	envInt("PORT", &cfg.Server.Port, errf)
	envDur("SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout, errf)

	if v, ok := os.LookupEnv("STORAGE_BACKEND"); ok {
		cfg.Storage.Backend = StorageBackend(strings.ToLower(strings.TrimSpace(v)))
	}
	envStr("SQLITE_PATH", &cfg.Storage.SQLitePath)
	envDur("SQLITE_BUSY_TIMEOUT", &cfg.Storage.BusyTimeout, errf)
	envInt("STORAGE_MAX_OPEN_CONNS", &cfg.Storage.MaxOpenConns, errf)

	if v, ok := os.LookupEnv("AUTH_MODE"); ok {
		cfg.Auth.Mode = AuthMode(strings.ToLower(strings.TrimSpace(v)))
	}
	envStr("AUTH_HEADER_NAME", &cfg.Auth.HeaderName)
	envStr("AUTH_WEBHOOK_URL", &cfg.Auth.WebhookURL)
	envStr("AUTH_WEBHOOK_SECRET", &cfg.Auth.WebhookSecret)
	envDur("AUTH_WEBHOOK_TIMEOUT", &cfg.Auth.WebhookTimeout, errf)
	envDur("AUTH_WEBHOOK_CACHE_TTL", &cfg.Auth.WebhookCacheTTL, errf)

	envStr("CONFLICT_RESOLVER_URL", &cfg.Conflict.ResolverURL)
	envStr("CONFLICT_RESOLVER_SECRET", &cfg.Conflict.ResolverSecret)
	envDur("CONFLICT_RESOLVER_TIMEOUT", &cfg.Conflict.Timeout, errf)

	envStr("WEBHOOK_SECRET", &cfg.Webhooks.Secret)
	envDur("WEBHOOK_TIMEOUT", &cfg.Webhooks.Timeout, errf)
	envInt("WEBHOOK_MAX_RETRIES", &cfg.Webhooks.MaxRetries, errf)
	applyWebhookEventEnv(cfg)

	envInt("SNAPSHOT_INTERVAL_OPS", &cfg.Snapshot.IntervalOps, errf)

	envStr("LOG_LEVEL", &cfg.Log.Level)
	envStr("LOG_FORMAT", &cfg.Log.Format)

	if len(errs) > 0 {
		return fmt.Errorf("invalid environment configuration: %s", strings.Join(errs, "; "))
	}
	return nil
}

// applyWebhookEventEnv maps environment variables of the form
// WEBHOOK_<EVENT>_URL to Events["<event>"]. This keeps the event set open-ended
// so new events can be wired up without a code change. For example
// WEBHOOK_ON_DOCUMENT_CREATED_URL sets Events["on_document_created"].
func applyWebhookEventEnv(cfg *Config) {
	const prefix = "WEBHOOK_"
	const suffix = "_URL"
	if cfg.Webhooks.Events == nil {
		cfg.Webhooks.Events = map[string]string{}
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, val := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		name := key[len(prefix) : len(key)-len(suffix)]
		// Reserved non-event keys under the WEBHOOK_ prefix.
		if name == "SECRET" || name == "TIMEOUT" || name == "MAX_RETRIES" {
			continue
		}
		if name == "" {
			continue
		}
		cfg.Webhooks.Events[strings.ToLower(name)] = val
	}
}

// Validate checks that the resolved configuration is internally consistent.
func (c Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port %d out of range 1-65535", c.Server.Port)
	}

	switch c.Storage.Backend {
	case StorageSQLite:
		if strings.TrimSpace(c.Storage.SQLitePath) == "" {
			return fmt.Errorf("sqlite_path must not be empty for sqlite backend")
		}
	default:
		return fmt.Errorf("unsupported storage backend %q", c.Storage.Backend)
	}
	if c.Storage.MaxOpenConns < 1 {
		return fmt.Errorf("storage max_open_conns must be >= 1, got %d", c.Storage.MaxOpenConns)
	}

	switch c.Auth.Mode {
	case AuthModeNone:
	case AuthModeHeader:
		if strings.TrimSpace(c.Auth.HeaderName) == "" {
			return fmt.Errorf("auth header_name must not be empty in header mode")
		}
	case AuthModeWebhook:
		if strings.TrimSpace(c.Auth.WebhookURL) == "" {
			return fmt.Errorf("auth webhook_url is required in webhook mode")
		}
	default:
		return fmt.Errorf("unsupported auth mode %q", c.Auth.Mode)
	}

	if c.Snapshot.IntervalOps < 1 {
		return fmt.Errorf("snapshot interval_ops must be >= 1, got %d", c.Snapshot.IntervalOps)
	}
	if c.Webhooks.MaxRetries < 0 {
		return fmt.Errorf("webhook max_retries must be >= 0, got %d", c.Webhooks.MaxRetries)
	}

	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported log level %q", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("unsupported log format %q", c.Log.Format)
	}

	return nil
}

// Addr returns the host:port the server should listen on.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// --- environment helpers ----------------------------------------------------

func envStr(key string, dst *string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func envInt(key string, dst *int, errf func(string, ...any)) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		errf("%s=%q is not a valid integer", key, v)
		return
	}
	*dst = n
}

func envDur(key string, dst *time.Duration, errf func(string, ...any)) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		errf("%s=%q is not a valid duration (e.g. 3s, 500ms)", key, v)
		return
	}
	*dst = d
}
