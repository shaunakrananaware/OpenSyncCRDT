# Deploying on Fly.io

OpenSyncCRDT ships a ready-to-use [`fly.toml`](../../deploy/fly/fly.toml).

## Prerequisites

- A [Fly.io](https://fly.io) account and the [`flyctl`](https://fly.io/docs/flyctl/install/)
  CLI (`fly auth login`).

## Deploy

From the repository root:

```bash
fly launch --copy-config --dockerfile deploy/docker/Dockerfile
# or, if the app already exists:
fly deploy --config deploy/fly/fly.toml
```

The bundled `fly.toml` exposes the HTTP/WebSocket service on port 8080 and wires
the `/health` and `/ready` checks. Review it and adjust the app name, region,
and VM size to your needs.

## Persistence (SQLite)

SQLite needs a persistent volume. Create one and mount it at `/data`:

```bash
fly volumes create opensync_data --size 1
```

Ensure the `[mounts]` section in `fly.toml` mounts that volume at `/data` (which
is `DATA_DIR` in the image). Single-node SQLite on one machine is the simplest
setup; do **not** run multiple machines against the same SQLite volume.

## Configuration

Set configuration as Fly secrets/env. Secrets (auth/webhook signing keys,
database URLs) should use `fly secrets`:

```bash
fly secrets set \
  STORAGE_BACKEND=postgres \
  STORAGE_URL=postgres://user:pass@host:5432/dbname \
  WEBHOOK_SECRET=... \
  AUTH_MODE=webhook AUTH_WEBHOOK_URL=https://yourapp.com/verify AUTH_WEBHOOK_SECRET=...
```

Non-secret values can go in the `[env]` block of `fly.toml`. See the
[configuration reference](../configuration.md).

## Scaling out

To run more than one machine, switch to a shared database and enable clustering:

```bash
fly secrets set CLUSTER_MODE=true CLUSTER_REDIS_URL=redis://... \
  STORAGE_BACKEND=postgres STORAGE_URL=postgres://user:pass@host:5432/dbname
fly scale count 2
```

Because sync connections are long-lived WebSockets, keep client affinity in mind
— see [clustering and sticky sessions](kubernetes.md#clustering-and-sticky-sessions).
Do not scale a SQLite deployment past one machine.
