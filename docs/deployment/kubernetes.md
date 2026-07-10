# Deploying on Kubernetes

OpenSyncCRDT ships plain manifests and a Helm chart.

- Raw manifests: [`deploy/kubernetes/`](../../deploy/kubernetes)
- Helm chart: [`deploy/helm/opensynccrdt/`](../../deploy/helm/opensynccrdt)

## Raw manifests

The [`deploy/kubernetes/`](../../deploy/kubernetes) directory contains:

| File | Purpose |
| ---- | ------- |
| `namespace.yaml` | Dedicated namespace. |
| `serviceaccount.yaml` | Service account for the pods. |
| `configmap.yaml` | Non-secret configuration (env vars). |
| `secret.yaml` | Secret **template** (fill in before applying). |
| `persistentvolumeclaim.yaml` | Data volume for the SQLite single-node case. |
| `deployment.yaml` | The Deployment (probes, resources, rolling update, volume). |
| `service.yaml` | ClusterIP service. |
| `hpa.yaml` | HorizontalPodAutoscaler. |
| `ingress.yaml` | Ingress with sticky-session annotations. |
| `poddisruptionbudget.yaml` | PodDisruptionBudget. |

Apply them (edit the ConfigMap/Secret first):

```bash
kubectl apply -f deploy/kubernetes/namespace.yaml
kubectl apply -f deploy/kubernetes/
```

The Deployment defines:

- Liveness probe `GET /health` every 10s and readiness probe `GET /ready` every
  5s.
- Resource requests and limits.
- A rolling update strategy and a pod disruption budget.
- Environment sourced from the ConfigMap and Secret.
- A mounted data volume.

## Helm

```bash
helm install opensynccrdt ./deploy/helm/opensynccrdt \
  --set storage.backend=postgres \
  --set storage.url=postgres://user:pass@host/dbname \
  --set auth.mode=webhook \
  --set auth.webhookUrl=https://yourapp.com/verify
```

With the default values the chart deploys a SQLite single node with a PVC. When
you set a Postgres/MySQL backend, the PVC is omitted and the connection URL is
stored in a Secret; the HPA and Ingress are enabled for the scale-out case. See
[`deploy/helm/opensynccrdt/values.yaml`](../../deploy/helm/opensynccrdt/values.yaml)
for every knob.

## Clustering and sticky sessions

Single node is the default. To scale horizontally, enable cluster mode and give
every node a shared database and a Redis instance:

```bash
--set cluster.mode=true \
--set cluster.redisUrl=redis://redis:6379 \
--set storage.backend=postgres \
--set storage.url=postgres://user:pass@host/dbname
```

In cluster mode all nodes share one Postgres/MySQL database and fan operations
out to each other over Redis pub/sub, so a client on node A and a client on
node B stay in sync. **SQLite cannot be used in cluster mode** — the server
refuses to start with `CLUSTER_MODE=true` and `STORAGE_BACKEND=sqlite`.

> **Sticky sessions are required.** WebSocket connections are long-lived and
> stateful per node, so the load balancer must pin each client to one backend
> (session affinity). The bundled `ingress.yaml` sets the Nginx ingress
> annotation:
>
> ```yaml
> nginx.ingress.kubernetes.io/affinity: cookie
> ```
>
> Without sticky sessions, reconnecting clients bounce between nodes and
> subscriptions churn. Redis still delivers cross-node broadcasts correctly, but
> affinity keeps connection handling efficient.

## Probes recap

| Probe | Path | Behaviour |
| ----- | ---- | --------- |
| Liveness | `/health` | `200` while the process is serving. |
| Readiness | `/ready` | `200` when storage is reachable, `503` otherwise. |
