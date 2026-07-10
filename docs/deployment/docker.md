# Deploying with Docker

OpenSyncCRDT is a single static binary, published as three image variants:

| Tag | Base | Use |
| --- | ---- | --- |
| `opensynccrdt/server:latest` | Debian slim | Standard, shell + tools for debugging. |
| `opensynccrdt/server:alpine` | Alpine | Minimal image size. |
| `opensynccrdt/server:distroless` | distroless/static | Security-hardened, no shell. |

All three run the same fully-static binary; pick by your size/hardening
preference. The Dockerfiles live in [`deploy/docker/`](../../deploy/docker).

## Quickstart

```bash
docker run -p 8080:8080 opensynccrdt/server
```

That's a working server on SQLite. Without a volume, data is lost when the
container is removed.

## Persistence

SQLite writes to `/data/opensynccrdt.db` inside the container (`DATA_DIR`
defaults to `/data` in the images). Mount a volume to persist it:

```bash
docker run -p 8080:8080 -v ./data:/data opensynccrdt/server
```

## With Postgres

```bash
docker run -p 8080:8080 \
  -e STORAGE_BACKEND=postgres \
  -e STORAGE_URL=postgres://user:pass@host/dbname \
  opensynccrdt/server
```

The schema is created automatically on first run.

## Full configuration example

```bash
docker run -p 8080:8080 \
  -e AUTH_MODE=webhook \
  -e AUTH_WEBHOOK_URL=https://yourapp.com/verify \
  -e AUTH_WEBHOOK_SECRET=secret \
  -e WEBHOOK_SECRET=secret \
  -e WEBHOOK_ON_DOCUMENT_UPDATED_URL=https://yourapp.com/hooks \
  -e STORAGE_BACKEND=postgres \
  -e STORAGE_URL=postgres://user:pass@host/dbname \
  -e LOG_LEVEL=info \
  -e LOG_FORMAT=json \
  -v ./data:/data \
  opensynccrdt/server
```

See the [configuration reference](../configuration.md) for every variable.

## Docker Compose

The repository ships three compose files:

- [`docker-compose.yml`](../../docker-compose.yml) — SQLite, single node. The
  simplest persistent setup.
- [`docker-compose.postgres.yml`](../../docker-compose.postgres.yml) — a single
  node backed by Postgres.
- [`docker-compose.cluster.yml`](../../docker-compose.cluster.yml) — two nodes
  behind an Nginx sticky-session load balancer, sharing Postgres and using Redis
  for cross-node broadcast. This demonstrates multi-node clustering end to end.

```bash
docker compose up                                # SQLite single node
docker compose -f docker-compose.postgres.yml up # Postgres single node
docker compose -f docker-compose.cluster.yml up  # 2-node cluster
```

The cluster file wires the Nginx config in
[`deploy/docker/nginx-cluster.conf`](../../deploy/docker/nginx-cluster.conf),
which pins WebSocket connections to a backend with a cookie — clustering
**requires** sticky sessions (see [Kubernetes](kubernetes.md) for the same
requirement in an ingress).

## Building the images yourself

```bash
make docker-build     # builds all three variants locally
```

> **Build note:** the Linux binary links against **glibc**, not musl — the
> Automerge core references glibc-only symbols. The Dockerfiles build on a
> Debian/glibc stage and produce a fully static binary that runs unchanged on
> Debian, Alpine, and distroless. See
> [architecture](../contributing/architecture.md#build-and-platforms).

## Health checks

Point your orchestrator's probes at:

- Liveness: `GET /health`
- Readiness: `GET /ready` (returns `503` when storage is unreachable)
