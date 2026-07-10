# Configuration reference

Every configurable behaviour is set via **environment variables** or an optional
**YAML config file** — never a code change.

## Precedence

From lowest to highest:

```
built-in defaults  <  config file  <  environment variables
```

So you can ship a config file with a deployment and still override any single
value at runtime with an environment variable.

## Config file location

When `--config` is not given, these locations are tried in order and the first
that exists is loaded:

1. `./opensynccrdt.yaml` (current directory)
2. `/etc/opensynccrdt/config.yaml`

Override the path with the `--config` flag:

```bash
opensynccrdt --config /path/to/config.yaml
```

Notes:

- A missing file at a **default** location is not an error (defaults + env are
  used). A missing file at an **explicitly requested** `--config` path **is** an
  error.
- An empty (or whitespace-only) config file is a valid no-op.
- Unknown keys in the config file are rejected (they're almost always typos).

A fully-annotated example is in
[`config.example.yaml`](../config.example.yaml). The YAML structure mirrors the
sections below (`server:`, `storage:`, `auth:`, …).

## Reference

Durations use Go syntax (`3s`, `500ms`, `1m`). Booleans are `true`/`false`.

### Server

| Env var      | YAML            | Default   | Description |
| ------------ | --------------- | --------- | ----------- |
| `HOST`       | `server.host`   | `0.0.0.0` | Listen address. |
| `PORT`       | `server.port`   | `8080`    | Listen port (1–65535). |
| `LOG_LEVEL`  | `log.level`     | `info`    | `debug`, `info`, `warn`, or `error`. |
| `LOG_FORMAT` | `log.format`    | `json`    | `json` (log aggregators) or `text` (human-readable). |
| `DATA_DIR`   | `storage.data_dir` | `./data` | Directory for the SQLite database file. |

### TLS

| Env var          | YAML            | Default | Description |
| ---------------- | --------------- | ------- | ----------- |
| `TLS_ENABLED`    | `tls.enabled`   | `false` | Terminate TLS in the binary itself. |
| `TLS_CERT_FILE`  | `tls.cert_file` | —       | PEM certificate path (required when TLS is enabled). |
| `TLS_KEY_FILE`   | `tls.key_file`  | —       | PEM private key path (required when TLS is enabled). |

When `TLS_ENABLED=true`, both cert and key must be set or startup fails.

### Storage

| Env var                      | YAML                        | Default   | Description |
| ---------------------------- | --------------------------- | --------- | ----------- |
| `STORAGE_BACKEND`            | `storage.backend`           | `sqlite`  | `sqlite`, `postgres`, or `mysql`. |
| `STORAGE_URL`                | `storage.url`               | —         | Connection string (required for postgres/mysql). |
| `STORAGE_SNAPSHOT_INTERVAL`  | `storage.snapshot_interval` | `100`     | Take a full-state snapshot every N committed ops (≥ 1). |
| `STORAGE_POSTGRES_MAX_CONNS` | `storage.postgres_max_conns`| `10`      | Postgres pool max connections. |
| `STORAGE_POSTGRES_SSL_MODE`  | `storage.postgres_ssl_mode` | `require` | Postgres `sslmode`. |
| `STORAGE_MYSQL_MAX_CONNS`    | `storage.mysql_max_conns`   | `10`      | MySQL pool max connections. |

Connection-string formats:

- Postgres: `postgres://user:pass@host:5432/dbname`
- MySQL: `mysql://user:pass@host:3306/dbname`

All backends create their schema and run pending migrations automatically on
first start. See [architecture](contributing/architecture.md) for details.

### Auth

| Env var                   | YAML                     | Default     | Description |
| ------------------------- | ------------------------ | ----------- | ----------- |
| `AUTH_MODE`               | `auth.mode`              | `none`      | `none`, `header`, or `webhook`. |
| `AUTH_HEADER_NAME`        | `auth.header_name`       | `X-User-ID` | Trusted identity header (header mode). |
| `AUTH_WEBHOOK_URL`        | `auth.webhook_url`       | —           | Verification endpoint (required in webhook mode). |
| `AUTH_WEBHOOK_SECRET`     | `auth.webhook_secret`    | —           | HMAC-SHA256 signing secret for the verify request. |
| `AUTH_WEBHOOK_TIMEOUT`    | `auth.webhook_timeout`   | `3s`        | Verify request timeout. |
| `AUTH_WEBHOOK_CACHE_TTL`  | `auth.webhook_cache_ttl` | `60s`       | Cache allow/deny per token+doc; `0` disables caching. |

See [Authentication](auth.md) for the full behaviour of each mode.

### Webhooks

| Env var               | YAML                  | Default | Description |
| --------------------- | --------------------- | ------- | ----------- |
| `WEBHOOK_SECRET`      | `webhooks.secret`     | —       | Shared HMAC-SHA256 signing secret for all event webhooks. |
| `WEBHOOK_TIMEOUT`     | `webhooks.timeout`    | `5s`    | Per-request timeout. |
| `WEBHOOK_MAX_RETRIES` | `webhooks.max_retries`| `3`     | Retry attempts with exponential backoff. |
| `WEBHOOK_ON_DOCUMENT_CREATED_URL`    | `webhooks.events.on_document_created`    | — | URL for the event (unset = skipped). |
| `WEBHOOK_ON_DOCUMENT_UPDATED_URL`    | `webhooks.events.on_document_updated`    | — | URL for the event. |
| `WEBHOOK_ON_DOCUMENT_DELETED_URL`    | `webhooks.events.on_document_deleted`    | — | URL for the event. |
| `WEBHOOK_ON_CLIENT_CONNECTED_URL`    | `webhooks.events.on_client_connected`    | — | URL for the event. |
| `WEBHOOK_ON_CLIENT_DISCONNECTED_URL` | `webhooks.events.on_client_disconnected` | — | URL for the event. |
| `WEBHOOK_ON_SYNC_ERROR_URL`          | `webhooks.events.on_sync_error`          | — | URL for the event. |

Point all events at one URL and differentiate by the `X-OpenSyncCRDT-Event`
header, or use a different URL per event. See [Webhooks](webhooks.md).

### Conflict resolution

| Env var                     | YAML                     | Default | Description |
| --------------------------- | ------------------------ | ------- | ----------- |
| `CONFLICT_RESOLVER_URL`     | `conflict.resolver_url`  | —       | Custom resolver endpoint (empty = automatic Automerge merge). |
| `CONFLICT_RESOLVER_SECRET`  | `conflict.resolver_secret` | —     | HMAC-SHA256 signing secret. |
| `CONFLICT_RESOLVER_TIMEOUT` | `conflict.timeout`       | `5s`    | Timeout; on timeout/error, fall back to automatic merge and log a warning. |

### Clustering

| Env var             | YAML              | Default | Description |
| ------------------- | ----------------- | ------- | ----------- |
| `CLUSTER_MODE`      | `cluster.mode`    | `false` | Enable multi-node clustering. |
| `CLUSTER_BACKEND`   | `cluster.backend` | `redis` | Cross-node bus (only `redis` supported). |
| `CLUSTER_REDIS_URL` | `cluster.redis_url` | —     | Redis URL (required when clustering; e.g. `redis://host:6379`). |

In cluster mode the storage backend must be Postgres or MySQL — **SQLite is
rejected** (single-writer limitation). See
[deployment/kubernetes.md](deployment/kubernetes.md).

### Connection limits

| Env var                  | YAML                          | Default   | Description |
| ------------------------ | ----------------------------- | --------- | ----------- |
| `MAX_CONNECTIONS`        | `limits.max_connections`      | `10000`   | Max concurrent WebSocket connections. |
| `MAX_MESSAGE_SIZE_BYTES` | `limits.max_message_size_bytes` | `1048576` | Max inbound WebSocket message size (1 MiB). |
| `PING_INTERVAL`          | `limits.ping_interval`        | `30s`     | Keepalive ping interval (`0` disables). |
| `PONG_TIMEOUT`           | `limits.pong_timeout`         | `10s`     | Time to wait for a pong before closing. |
| `WRITE_TIMEOUT`          | `limits.write_timeout`        | `10s`     | Per-write deadline. |
| `READ_TIMEOUT`           | `limits.read_timeout`         | `0`       | HTTP read timeout; `0` = none. |

### CORS

| Env var                | YAML                  | Default | Description |
| ---------------------- | --------------------- | ------- | ----------- |
| `CORS_ALLOWED_ORIGINS` | `cors.allowed_origins`| `*`     | Comma-separated origins (env) / list (YAML). Lock down in production. |

`CORS_ALLOWED_ORIGINS` also governs which origins may open the `/sync`
WebSocket. `*` allows any origin.

### Management API

| Env var                   | YAML                 | Default | Description |
| ------------------------- | -------------------- | ------- | ----------- |
| `MANAGEMENT_API_ENABLED`  | `management.enabled` | `true`  | Serve the `/api/v1/` routes. |
| `MANAGEMENT_API_KEY`      | `management.key`     | —       | If set, all `/api/v1/` requests require `Authorization: Bearer <key>`. |

`/health` and `/ready` are always available and never require the key, even when
the management API is disabled.
