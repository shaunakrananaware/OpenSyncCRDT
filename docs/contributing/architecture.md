# Architecture

A map of the codebase and the key design decisions, for contributors.

## Package layout

```
cmd/opensynccrdt/      Entry point: load config, build engine, run HTTP lifecycle.
internal/
  config/              All configuration (env + YAML), defaults, validation.
  storage/             Store interface + sqlite/postgres/mysql backends + conformance suite.
  crdt/                Automerge wrapper and the conflict resolver.
  sync/                The sync engine: apply, persist, snapshot, replay, broadcast.
  server/              WebSocket transport: upgrade, per-connection loops, the hub.
  auth/                The three authenticators (none/header/webhook).
  webhook/             HMAC signing + the fire-and-forget event dispatcher.
  cluster/             Redis-backed node registry and cross-node pub/sub.
  api/                 REST management routes + health/ready + metrics.
  metrics/             Prometheus registry.
pkg/
  engine/              Public embedding API (the only supported public surface).
  protocol/            WebSocket envelopes + the wire codec.
```

`internal/` is implementation detail and may change without notice. The only
stable public API for embedding is `pkg/engine` (and the message/codec types in
`pkg/protocol`).

## Wiring

`pkg/engine.New` assembles the whole object graph and is shared by both the
standalone binary and embedders:

1. Open the configured `storage.Store`.
2. Build the `auth.Authenticator` for `AUTH_MODE`.
3. Build the metrics registry, the webhook `Dispatcher`, and the conflict
   `Resolver`.
4. Build the `server.Server` (owns the hub and the `/sync` handler).
5. If `CLUSTER_MODE`, join the cluster; the cluster node becomes the broadcaster
   and a subscription observer (so a node subscribes to a document's Redis
   channel only while it serves that document). Otherwise the hub broadcasts
   directly.
6. Build the `sync.Engine` (storage + broadcaster + resolver + dispatcher) and
   the REST `api.API`.

`Engine.Handler()` returns the full HTTP handler (REST + probes + `/sync`);
`Engine.WebSocketHandler()` returns just the `/sync` upgrade handler for mounting
at a custom path.

## The storage interface

Every backend implements one interface (`internal/storage`): append an op, read
ops since/in-range, save/get snapshots, get the latest seq, and document CRUD
plus a health check. Operations are stored as opaque Automerge change-set bytes
with a per-document monotonic sequence number.

The **storage backend is the only thing that differs** between deployments — all
sync logic, conflict resolution, WebSocket handling, and webhook dispatch are
identical regardless of backend. A shared conformance suite
(`internal/storage/conformance_test.go`) runs against SQLite, Postgres, and
MySQL so they stay behaviourally identical. Each backend creates its schema and
runs migrations automatically on first start.

Cluster mode requires a shared Postgres/MySQL database; SQLite is rejected at
startup because it is single-writer.

## The sync engine

`internal/sync` is the heart:

- **Apply + persist:** an inbound change set is applied to the server-side
  Automerge document and appended to storage, yielding a `seq`.
- **Snapshot:** every `STORAGE_SNAPSHOT_INTERVAL` committed ops (default 100) the
  full Automerge state is snapshotted so startup replays only the tail of the
  log, not the whole history.
- **Replay:** on `subscribe`, everything after the subscriber's `last_seq` is
  streamed back in batches.
- **Broadcast:** committed changes are fanned out to other local subscribers
  (via the hub) and, in cluster mode, published to Redis for other nodes.

The server treats change sets as opaque — all CRDT semantics live in Automerge.

## Transport

`internal/server` upgrades `/sync` to a WebSocket (using `github.com/coder/websocket`).
Each connection runs a read loop, a write loop, and a keepalive loop; the `hub`
tracks per-document subscriptions and routes broadcasts, skipping the
originating session to avoid echo. Connection slots are reserved up front to
enforce `MAX_CONNECTIONS`, and slow clients are dropped rather than allowed to
stall the engine.

## Clustering

`internal/cluster` registers each node in Redis (`opensynccrdt:nodes:<uuid>`,
30s TTL refreshed by a 10s heartbeat) and publishes/subscribes per-document
channels (`opensynccrdt:doc:<id>`). A node only subscribes to a document's
channel while it has a local subscriber for it, which the hub reports through the
subscription-observer hook. `/api/v1/nodes` reads the live registry.

## Build and platforms

Supported platforms: `linux/{amd64,arm64}` and `darwin/{amd64,arm64}`.

CGO is required — both SQLite (`mattn/go-sqlite3`) and the Automerge core are cgo
dependencies; everything else is pure Go. The Automerge dependency ships
prebuilt Rust static archives that reference **glibc** LFS64 symbols
(`stat64`/`open64`/`fstat64`) which **musl lacks**. Consequences:

- Linux release binaries are built against glibc (the `dist-linux-*` Makefile
  targets use `zig cc -target *-linux-gnu` plus `-tags netgo` for a pure-Go DNS
  resolver). The result is still a fully static binary that runs on Debian,
  Alpine, and distroless alike.
- **Windows is not supported.** Automerge ships archives and cgo `LDFLAGS` only
  for the four platforms above; there is no Windows archive to link against, and
  building a custom CRDT is out of scope.

See the [`Makefile`](../../Makefile) header for the exact flags and the
[Docker deployment guide](../deployment/docker.md#building-the-images-yourself).

## Observability

Structured logging uses the stdlib `log/slog` (`LOG_FORMAT=json|text`), no
external logging library. Prometheus metrics are exposed at `/api/v1/metrics`;
the series are listed in the [REST reference](../api-reference/rest.md#get-apiv1metrics).
