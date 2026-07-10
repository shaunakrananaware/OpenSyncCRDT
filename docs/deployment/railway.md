# Deploying on Railway

OpenSyncCRDT ships a [`railway.json`](../../deploy/railway/railway.json) for
[Railway](https://railway.app).

## Deploy

1. Create a new Railway project from your fork/clone of the repository.
2. Railway reads [`deploy/railway/railway.json`](../../deploy/railway/railway.json),
   which builds the Docker image and runs the server.
3. Expose the service and note the generated public domain.

Railway builds from the standard [`deploy/docker/Dockerfile`](../../deploy/docker/Dockerfile).

## Configuration

Set configuration as service variables in the Railway dashboard (or via the
CLI). At minimum you'll usually want:

```
LOG_FORMAT=json
STORAGE_BACKEND=postgres
STORAGE_URL=${{Postgres.DATABASE_URL}}
```

Railway's Postgres plugin exposes a `DATABASE_URL` you can reference directly.
See the [configuration reference](../configuration.md) for the full set.

## Persistence

If you stay on the default SQLite backend, attach a Railway **volume** and mount
it at `/data` (the image's `DATA_DIR`) so the database survives redeploys. For
anything beyond a single instance, use the Postgres plugin instead — SQLite
cannot be shared across instances.

## Scaling

To run multiple replicas, add a Redis plugin and enable clustering:

```
CLUSTER_MODE=true
CLUSTER_REDIS_URL=${{Redis.REDIS_URL}}
STORAGE_BACKEND=postgres
STORAGE_URL=${{Postgres.DATABASE_URL}}
```

Sync uses long-lived WebSockets, so keep session affinity in mind when placing a
load balancer in front — see
[clustering and sticky sessions](kubernetes.md#clustering-and-sticky-sessions).
