# REST API reference

The REST management API is a secondary surface for document management,
inspection, and observability. Real-time sync happens over the
[WebSocket endpoint](websocket.md), not here.

- Base path: `/api/v1/`
- All endpoints accept and return JSON (except `/api/v1/metrics`, which returns
  Prometheus text exposition).
- API versioning is in the URL path; v1 is designed to be extended without a
  breaking v2.

## Availability and auth

- `/health` and `/ready` are **always** served and never require auth.
- The `/api/v1/` routes are served only when `MANAGEMENT_API_ENABLED=true` (the
  default).
- When `MANAGEMENT_API_KEY` is set, every `/api/v1/` request must send
  `Authorization: Bearer <key>`; otherwise the response is `401`.

```bash
curl -H "Authorization: Bearer $MANAGEMENT_API_KEY" localhost:8080/api/v1/docs
```

## Probes

### `GET /health`

Liveness. Always `200` while the process is serving.

```json
{ "status": "ok" }
```

### `GET /ready`

Readiness. `200` when storage is reachable, `503` otherwise.

```json
{ "status": "ready" }
```

## Documents

### `POST /api/v1/docs`

Create a document (optionally with metadata). Documents are also created
implicitly on first sync, so this is only needed to pre-create or attach
metadata.

Request:

```json
{ "doc_id": "my-doc", "metadata": { "title": "My doc" } }
```

Responses: `201` with the document view; `400` if `doc_id` is missing; `409` if
it already exists.

```json
{
  "id": "my-doc",
  "metadata": { "title": "My doc" },
  "latest_seq": 0,
  "created_at": "2026-07-11T12:00:00Z",
  "updated_at": "2026-07-11T12:00:00Z"
}
```

### `GET /api/v1/docs`

List documents. Query parameters (all optional):

| Param | Meaning |
| ----- | ------- |
| `limit` | Max documents to return. |
| `offset` | Skip this many. |
| `updated_since` | RFC3339 timestamp; only documents updated at/after it. |

```json
{ "documents": [ { "id": "my-doc", "metadata": {}, "latest_seq": 12, "created_at": "...", "updated_at": "..." } ] }
```

### `GET /api/v1/docs/{id}`

Document metadata. `404` if not found.

```json
{ "id": "my-doc", "metadata": {}, "latest_seq": 12, "created_at": "...", "updated_at": "..." }
```

### `DELETE /api/v1/docs/{id}`

Delete a document and all its operations. Fires the `on_document_deleted`
webhook. `404` if not found.

```json
{ "deleted": true, "doc_id": "my-doc" }
```

### `GET /api/v1/docs/{id}/history`

Operation history. Query parameters: `after` (only ops with `seq` greater than
this) and `limit`. Payloads are base64-encoded Automerge change sets.

```json
{
  "doc_id": "my-doc",
  "ops": [
    { "seq": 1, "session_id": "abc", "payload": "<base64>", "created_at": "..." }
  ]
}
```

### `POST /api/v1/docs/{id}/snapshot`

Trigger a snapshot of the current Automerge state. Returns the sequence the
snapshot was taken at.

```json
{ "doc_id": "my-doc", "seq": 12 }
```

### `GET /api/v1/docs/{id}/export`

Export the full Automerge document state as a base64-encoded binary blob. Load
it on a client with `Automerge.load(...)`.

```json
{ "doc_id": "my-doc", "state": "<base64 automerge state>" }
```

## Observability

### `GET /api/v1/metrics`

Prometheus text exposition (`Content-Type: text/plain; version=0.0.4`). Exposed
series include:

```
opensynccrdt_connections_total
opensynccrdt_connections_active
opensynccrdt_operations_total          # labelled by doc_id
opensynccrdt_operations_errors_total
opensynccrdt_webhook_calls_total       # labelled by event
opensynccrdt_webhook_errors_total      # labelled by event
opensynccrdt_storage_query_duration_seconds
opensynccrdt_replay_operations_total
```

### `GET /api/v1/nodes`

List cluster nodes. In cluster mode this reads the live Redis registry of alive
nodes; in single-node mode it reports just this node.

```json
{
  "cluster_mode": true,
  "nodes": [
    { "id": "node-uuid", "addr": "10.0.0.5:8080", "started_at": "2026-07-11T12:00:00Z" }
  ]
}
```

## Error shape

Errors return the appropriate HTTP status with a JSON body:

```json
{ "error": "document not found" }
```
