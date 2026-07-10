# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- CRDT sync engine built on [automerge-go](https://github.com/automerge/automerge-go):
  the server stores, orders, and routes opaque Automerge binary change sets and
  takes periodic state snapshots.
- WebSocket sync endpoint (`/sync`) with JSON envelopes (`subscribe`, `sync`,
  `ping` inbound; `sync`, `replay`, `ack`, `error`, `pong` outbound) and offline
  catch-up via batched replay.
- REST management API under `/api/v1/` (create/list/get/delete documents,
  history, manual snapshot, export, metrics, cluster nodes) plus Kubernetes
  `/health` and `/ready` probes.
- Three storage backends behind one interface — SQLite (default), PostgreSQL,
  and MySQL — selectable at runtime with no code changes; schema and migrations
  are created automatically on first run.
- Three auth modes — `none`, `header`, and `webhook` (with per-token/per-doc
  response caching) — selectable via `AUTH_MODE`.
- Outbound event webhooks (`on_document_created`, `on_document_updated`,
  `on_document_deleted`, `on_client_connected`, `on_client_disconnected`,
  `on_sync_error`) with HMAC-SHA256 signatures and retry-with-backoff.
- Optional custom conflict-resolver webhook with fall-back to automatic
  Automerge resolution on error or timeout.
- Multi-node clustering over Redis pub/sub with node discovery and a
  heartbeat-backed `/api/v1/nodes` registry.
- Prometheus metrics at `/api/v1/metrics` and structured `log/slog` logging
  (JSON or text).
- Embedding API (`pkg/engine`) for running the engine in-process on an existing
  Go HTTP mux.
- Full configuration surface via environment variables or a YAML config file.
- Deployment artifacts: three Docker image variants, Docker Compose files
  (SQLite / Postgres / cluster), Kubernetes manifests, a Helm chart, a systemd
  unit and `install.sh`, and one-click configs for Fly.io, Railway, Render, and
  Coolify.
- CI/CD workflows (CI, release, weekly security scan) and project documentation.

### Notes

- **Supported platforms:** `linux/amd64`, `linux/arm64`, `darwin/amd64`,
  `darwin/arm64`. Windows is not supported because the Automerge CRDT core
  ships prebuilt native archives only for those four platforms.
- The optional MessagePack WebSocket subprotocol (`opensync-msgpack`) is
  reserved but not yet implemented; clients that request it transparently fall
  back to JSON.

[Unreleased]: https://github.com/opensynccrdt/opensynccrdt/commits/main
