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
//
// The set of environment variables and their defaults matches the
// specification's configuration reference exactly.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AuthMode selects how incoming connections are authenticated.
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
	// StoragePostgres is the PostgreSQL backend.
	StoragePostgres StorageBackend = "postgres"
	// StorageMySQL is the MySQL backend.
	StorageMySQL StorageBackend = "mysql"
)

// Default config-file search paths, tried in order when --config is not given.
var defaultConfigPaths = []string{
	"./opensynccrdt.yaml",
	"/etc/opensynccrdt/config.yaml",
}

// Config is the fully-resolved configuration for a running instance.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	TLS        TLSConfig        `yaml:"tls"`
	Storage    StorageConfig    `yaml:"storage"`
	Auth       AuthConfig       `yaml:"auth"`
	Webhooks   WebhookConfig    `yaml:"webhooks"`
	Conflict   ConflictConfig   `yaml:"conflict"`
	Cluster    ClusterConfig    `yaml:"cluster"`
	Limits     LimitsConfig     `yaml:"limits"`
	CORS       CORSConfig       `yaml:"cors"`
	Management ManagementConfig `yaml:"management"`
	Log        LogConfig        `yaml:"log"`
}

// ServerConfig controls the HTTP/WebSocket listener.
type ServerConfig struct {
	Host string `yaml:"host"` // HOST
	Port int    `yaml:"port"` // PORT
	// ShutdownTimeout bounds graceful shutdown. Not spec-configurable; a fixed
	// operational default.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// TLSConfig controls optional TLS termination by the binary itself.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`   // TLS_ENABLED
	CertFile string `yaml:"cert_file"` // TLS_CERT_FILE
	KeyFile  string `yaml:"key_file"`  // TLS_KEY_FILE
}

// StorageConfig controls the persistence layer.
type StorageConfig struct {
	Backend StorageBackend `yaml:"backend"` // STORAGE_BACKEND
	// DataDir is where the SQLite database file lives (DATA_DIR). The database
	// path is DataDir/opensynccrdt.db.
	DataDir string `yaml:"data_dir"` // DATA_DIR
	// URL is the connection string for the postgres/mysql backends.
	URL string `yaml:"url"` // STORAGE_URL
	// SnapshotInterval takes a full-state snapshot every N committed ops.
	SnapshotInterval int    `yaml:"snapshot_interval"`  // STORAGE_SNAPSHOT_INTERVAL
	PostgresMaxConns int    `yaml:"postgres_max_conns"` // STORAGE_POSTGRES_MAX_CONNS
	PostgresSSLMode  string `yaml:"postgres_ssl_mode"`  // STORAGE_POSTGRES_SSL_MODE
	MySQLMaxConns    int    `yaml:"mysql_max_conns"`    // STORAGE_MYSQL_MAX_CONNS

	// BusyTimeout and MaxOpenConns tune the embedded SQLite driver. Fixed
	// operational defaults, not spec-configurable env vars.
	BusyTimeout  time.Duration `yaml:"busy_timeout"`
	MaxOpenConns int           `yaml:"max_open_conns"`
}

// AuthConfig controls connection authentication.
type AuthConfig struct {
	Mode            AuthMode      `yaml:"mode"`              // AUTH_MODE
	HeaderName      string        `yaml:"header_name"`       // AUTH_HEADER_NAME
	WebhookURL      string        `yaml:"webhook_url"`       // AUTH_WEBHOOK_URL
	WebhookSecret   string        `yaml:"webhook_secret"`    // AUTH_WEBHOOK_SECRET
	WebhookTimeout  time.Duration `yaml:"webhook_timeout"`   // AUTH_WEBHOOK_TIMEOUT
	WebhookCacheTTL time.Duration `yaml:"webhook_cache_ttl"` // AUTH_WEBHOOK_CACHE_TTL
}

// WebhookConfig controls outbound event webhooks. Events maps an event name
// (e.g. "on_document_created") to the URL that should receive it. An unset or
// empty URL means the event is silently skipped.
type WebhookConfig struct {
	Secret     string            `yaml:"secret"`      // WEBHOOK_SECRET
	Timeout    time.Duration     `yaml:"timeout"`     // WEBHOOK_TIMEOUT
	MaxRetries int               `yaml:"max_retries"` // WEBHOOK_MAX_RETRIES
	Events     map[string]string `yaml:"events"`      // WEBHOOK_<EVENT>_URL
}

// ConflictConfig controls the optional custom conflict-resolver webhook. When
// ResolverURL is empty, Automerge resolves conflicts automatically.
type ConflictConfig struct {
	ResolverURL    string        `yaml:"resolver_url"`    // CONFLICT_RESOLVER_URL
	ResolverSecret string        `yaml:"resolver_secret"` // CONFLICT_RESOLVER_SECRET
	Timeout        time.Duration `yaml:"timeout"`         // CONFLICT_RESOLVER_TIMEOUT
}

// ClusterConfig controls optional multi-node clustering.
type ClusterConfig struct {
	Mode     bool   `yaml:"mode"`      // CLUSTER_MODE
	Backend  string `yaml:"backend"`   // CLUSTER_BACKEND
	RedisURL string `yaml:"redis_url"` // CLUSTER_REDIS_URL
}

// LimitsConfig controls connection and message limits.
type LimitsConfig struct {
	MaxConnections      int           `yaml:"max_connections"`        // MAX_CONNECTIONS
	MaxMessageSizeBytes int64         `yaml:"max_message_size_bytes"` // MAX_MESSAGE_SIZE_BYTES
	PingInterval        time.Duration `yaml:"ping_interval"`          // PING_INTERVAL
	PongTimeout         time.Duration `yaml:"pong_timeout"`           // PONG_TIMEOUT
	WriteTimeout        time.Duration `yaml:"write_timeout"`          // WRITE_TIMEOUT
	ReadTimeout         time.Duration `yaml:"read_timeout"`           // READ_TIMEOUT (0 = none)
}

// CORSConfig controls Cross-Origin Resource Sharing for the HTTP surface.
type CORSConfig struct {
	// AllowedOrigins is the list of allowed origins, or ["*"] for any.
	AllowedOrigins []string `yaml:"allowed_origins"` // CORS_ALLOWED_ORIGINS
}

// ManagementConfig controls the REST management API.
type ManagementConfig struct {
	Enabled bool   `yaml:"enabled"` // MANAGEMENT_API_ENABLED
	Key     string `yaml:"key"`     // MANAGEMENT_API_KEY
}

// LogConfig controls logging.
type LogConfig struct {
	Level  string `yaml:"level"`  // LOG_LEVEL: debug, info, warn, error
	Format string `yaml:"format"` // LOG_FORMAT: json or text
}

// Default returns a Config populated with the specification's default values.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
		},
		TLS: TLSConfig{
			Enabled: false,
		},
		Storage: StorageConfig{
			Backend:          StorageSQLite,
			DataDir:          "./data",
			SnapshotInterval: 100,
			PostgresMaxConns: 10,
			PostgresSSLMode:  "require",
			MySQLMaxConns:    10,
			BusyTimeout:      5 * time.Second,
			MaxOpenConns:     1,
		},
		Auth: AuthConfig{
			Mode:            AuthModeNone,
			HeaderName:      "X-User-ID",
			WebhookTimeout:  3 * time.Second,
			WebhookCacheTTL: 60 * time.Second,
		},
		Webhooks: WebhookConfig{
			Timeout:    5 * time.Second,
			MaxRetries: 3,
			Events:     map[string]string{},
		},
		Conflict: ConflictConfig{
			Timeout: 5 * time.Second,
		},
		Cluster: ClusterConfig{
			Mode:    false,
			Backend: "redis",
		},
		Limits: LimitsConfig{
			MaxConnections:      10000,
			MaxMessageSizeBytes: 1048576,
			PingInterval:        30 * time.Second,
			PongTimeout:         10 * time.Second,
			WriteTimeout:        10 * time.Second,
			ReadTimeout:         0,
		},
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
		},
		Management: ManagementConfig{
			Enabled: true,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load resolves configuration from (in increasing precedence) defaults, an
// optional YAML config file, and environment variables. When path is empty the
// default config-file locations are tried in order; a missing file at a default
// location is not an error, but a missing file at an explicitly requested path
// is.
func Load(path string) (Config, error) {
	cfg := Default()

	explicit := path != ""
	if path == "" {
		path = findDefaultConfigFile()
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !explicit && os.IsNotExist(err) {
				// A default location vanished between stat and read; ignore.
			} else {
				return Config{}, fmt.Errorf("read config file %q: %w", path, err)
			}
		} else if strings.TrimSpace(string(data)) != "" {
			// An empty (or whitespace-only) file is a valid no-op. Otherwise
			// KnownFields guards against typos rather than silently ignoring them.
			dec := yaml.NewDecoder(strings.NewReader(string(data)))
			dec.KnownFields(true)
			if err := dec.Decode(&cfg); err != nil {
				return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
			}
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

func findDefaultConfigFile() string {
	for _, p := range defaultConfigPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// applyEnv overlays environment variables onto cfg. Only variables that are
// actually set take effect, preserving lower-precedence values otherwise.
func applyEnv(cfg *Config) error {
	var errs []string
	errf := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	// Server
	envStr("HOST", &cfg.Server.Host)
	envInt("PORT", &cfg.Server.Port, errf)

	// TLS
	envBool("TLS_ENABLED", &cfg.TLS.Enabled, errf)
	envStr("TLS_CERT_FILE", &cfg.TLS.CertFile)
	envStr("TLS_KEY_FILE", &cfg.TLS.KeyFile)

	// Storage
	if v, ok := os.LookupEnv("STORAGE_BACKEND"); ok {
		cfg.Storage.Backend = StorageBackend(strings.ToLower(strings.TrimSpace(v)))
	}
	envStr("DATA_DIR", &cfg.Storage.DataDir)
	envStr("STORAGE_URL", &cfg.Storage.URL)
	envInt("STORAGE_SNAPSHOT_INTERVAL", &cfg.Storage.SnapshotInterval, errf)
	envInt("STORAGE_POSTGRES_MAX_CONNS", &cfg.Storage.PostgresMaxConns, errf)
	envStr("STORAGE_POSTGRES_SSL_MODE", &cfg.Storage.PostgresSSLMode)
	envInt("STORAGE_MYSQL_MAX_CONNS", &cfg.Storage.MySQLMaxConns, errf)

	// Auth
	if v, ok := os.LookupEnv("AUTH_MODE"); ok {
		cfg.Auth.Mode = AuthMode(strings.ToLower(strings.TrimSpace(v)))
	}
	envStr("AUTH_HEADER_NAME", &cfg.Auth.HeaderName)
	envStr("AUTH_WEBHOOK_URL", &cfg.Auth.WebhookURL)
	envStr("AUTH_WEBHOOK_SECRET", &cfg.Auth.WebhookSecret)
	envDur("AUTH_WEBHOOK_TIMEOUT", &cfg.Auth.WebhookTimeout, errf)
	envDur("AUTH_WEBHOOK_CACHE_TTL", &cfg.Auth.WebhookCacheTTL, errf)

	// Webhooks
	envStr("WEBHOOK_SECRET", &cfg.Webhooks.Secret)
	envDur("WEBHOOK_TIMEOUT", &cfg.Webhooks.Timeout, errf)
	envInt("WEBHOOK_MAX_RETRIES", &cfg.Webhooks.MaxRetries, errf)
	applyWebhookEventEnv(cfg)

	// Conflict resolution
	envStr("CONFLICT_RESOLVER_URL", &cfg.Conflict.ResolverURL)
	envStr("CONFLICT_RESOLVER_SECRET", &cfg.Conflict.ResolverSecret)
	envDur("CONFLICT_RESOLVER_TIMEOUT", &cfg.Conflict.Timeout, errf)

	// Clustering
	envBool("CLUSTER_MODE", &cfg.Cluster.Mode, errf)
	envStr("CLUSTER_BACKEND", &cfg.Cluster.Backend)
	envStr("CLUSTER_REDIS_URL", &cfg.Cluster.RedisURL)

	// Connection limits
	envInt("MAX_CONNECTIONS", &cfg.Limits.MaxConnections, errf)
	envInt64("MAX_MESSAGE_SIZE_BYTES", &cfg.Limits.MaxMessageSizeBytes, errf)
	envDur("PING_INTERVAL", &cfg.Limits.PingInterval, errf)
	envDur("PONG_TIMEOUT", &cfg.Limits.PongTimeout, errf)
	envDur("WRITE_TIMEOUT", &cfg.Limits.WriteTimeout, errf)
	envDur("READ_TIMEOUT", &cfg.Limits.ReadTimeout, errf)

	// CORS
	if v, ok := os.LookupEnv("CORS_ALLOWED_ORIGINS"); ok {
		cfg.CORS.AllowedOrigins = splitCSV(v)
	}

	// Management API
	envBool("MANAGEMENT_API_ENABLED", &cfg.Management.Enabled, errf)
	envStr("MANAGEMENT_API_KEY", &cfg.Management.Key)

	// Logging
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

	if c.TLS.Enabled && (strings.TrimSpace(c.TLS.CertFile) == "" || strings.TrimSpace(c.TLS.KeyFile) == "") {
		return fmt.Errorf("tls enabled but cert_file/key_file not both set")
	}

	switch c.Storage.Backend {
	case StorageSQLite:
		if strings.TrimSpace(c.Storage.DataDir) == "" {
			return fmt.Errorf("data_dir must not be empty for sqlite backend")
		}
	case StoragePostgres, StorageMySQL:
		if strings.TrimSpace(c.Storage.URL) == "" {
			return fmt.Errorf("storage url is required for %s backend", c.Storage.Backend)
		}
	default:
		return fmt.Errorf("unsupported storage backend %q", c.Storage.Backend)
	}
	if c.Storage.SnapshotInterval < 1 {
		return fmt.Errorf("storage snapshot_interval must be >= 1, got %d", c.Storage.SnapshotInterval)
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

	if c.Webhooks.MaxRetries < 0 {
		return fmt.Errorf("webhook max_retries must be >= 0, got %d", c.Webhooks.MaxRetries)
	}

	// SQLite cannot be used in cluster mode (single-writer limitation).
	if c.Cluster.Mode && c.Storage.Backend == StorageSQLite {
		return fmt.Errorf("cluster mode requires postgres or mysql storage; sqlite cannot be used in a cluster")
	}
	if c.Cluster.Mode {
		if strings.ToLower(strings.TrimSpace(c.Cluster.Backend)) != "redis" {
			return fmt.Errorf("cluster backend %q unsupported; only redis is supported", c.Cluster.Backend)
		}
		if strings.TrimSpace(c.Cluster.RedisURL) == "" {
			return fmt.Errorf("cluster mode requires cluster redis_url (CLUSTER_REDIS_URL)")
		}
	}

	if c.Limits.MaxConnections < 1 {
		return fmt.Errorf("max_connections must be >= 1, got %d", c.Limits.MaxConnections)
	}
	if c.Limits.MaxMessageSizeBytes < 1 {
		return fmt.Errorf("max_message_size_bytes must be >= 1, got %d", c.Limits.MaxMessageSizeBytes)
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

func envInt64(key string, dst *int64, errf func(string, ...any)) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		errf("%s=%q is not a valid integer", key, v)
		return
	}
	*dst = n
}

func envBool(key string, dst *bool, errf func(string, ...any)) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		errf("%s=%q is not a valid boolean (true/false)", key, v)
		return
	}
	*dst = b
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

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
