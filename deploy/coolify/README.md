# Deploying OpenSyncCRDT on Coolify

[Coolify](https://coolify.io) is a self-hostable PaaS. OpenSyncCRDT deploys on
it as a plain Docker application — no Coolify-specific files are required, which
is why this directory is documentation only.

## Single node (SQLite)

1. In Coolify, create a new **Resource → Application** and point it at your fork
   of this repository.
2. Set the build pack to **Dockerfile** and the Dockerfile path to
   `deploy/docker/Dockerfile`.
3. Expose port **8080**.
4. Add a **Persistent Storage** volume mounted at `/data` so the SQLite
   database survives redeploys.
5. Set the health check path to `/health`.
6. Deploy.

The sync endpoint is then `wss://<your-domain>/sync` and the management API is
`https://<your-domain>/api/v1/`.

## Environment variables

Set these under the application's **Environment Variables** tab. The complete
list is in [`docs/configuration.md`](../../docs/configuration.md). Common ones:

| Variable          | Example                                        |
| ----------------- | ---------------------------------------------- |
| `STORAGE_BACKEND` | `postgres`                                     |
| `STORAGE_URL`     | `postgres://user:pass@host:5432/db`            |
| `AUTH_MODE`       | `header`                                        |
| `LOG_FORMAT`      | `json`                                          |

## Postgres-backed cluster

1. Add a **PostgreSQL** and a **Redis** database resource in Coolify.
2. Set `STORAGE_BACKEND=postgres`, `STORAGE_URL=<postgres connection string>`,
   `CLUSTER_MODE=true`, `CLUSTER_BACKEND=redis`, and
   `CLUSTER_REDIS_URL=<redis connection string>` on the application.
3. Scale the application to multiple instances.
4. Enable **sticky sessions** on the Coolify proxy (Traefik) so each WebSocket
   connection stays pinned to one instance. Cross-instance operation delivery is
   handled by Redis.
