# Getting started

This guide takes you from nothing to a running sync server and a first
synchronised document in a few minutes.

## 1. Run the server

The fastest path is Docker:

```bash
docker run -p 8080:8080 opensynccrdt/server
```

Or download a binary for your platform from the
[releases page](https://github.com/opensynccrdt/opensynccrdt/releases) and run
it:

```bash
./opensynccrdt
```

Either way you now have:

- a WebSocket sync endpoint at `ws://localhost:8080/sync`,
- a REST management API under `http://localhost:8080/api/v1/`,
- health/readiness probes at `/health` and `/ready`.

By default the server uses the embedded **SQLite** backend and accepts all
connections (`AUTH_MODE=none`). That's ideal for local development; see
[Authentication](auth.md) before exposing it publicly.

Confirm it's healthy:

```bash
curl localhost:8080/health   # {"status":"ok"}
curl localhost:8080/ready    # {"status":"ready"}
```

## 2. Persist data across restarts

SQLite writes to `DATA_DIR/opensynccrdt.db` (`DATA_DIR` defaults to `./data`).
With Docker, mount a volume so the database survives container restarts:

```bash
docker run -p 8080:8080 -v ./data:/data opensynccrdt/server
```

## 3. Create a document (optional)

Documents are created automatically the first time a change set is written to
them, so you don't have to pre-create anything. If you want to create one
explicitly (for example to attach metadata), use the management API:

```bash
curl -X POST localhost:8080/api/v1/docs \
  -H 'Content-Type: application/json' \
  -d '{"doc_id":"my-doc","metadata":{"title":"My first doc"}}'
```

See the [REST API reference](api-reference/rest.md) for the full surface.

## 4. Sync from a client

Real-time sync is a WebSocket connection to `/sync`. Messages are JSON envelopes
that carry Automerge binary change sets base64-encoded in a `payload` field. The
minimal flow is:

1. Connect to `ws://localhost:8080/sync` (optionally requesting the
   `opensync-json` subprotocol).
2. Send a `subscribe` envelope with your `doc_id`, a `session_id` you generate,
   and the highest `last_seq` you already have (`0` if new). The server replays
   everything you've missed as `replay` batches.
3. To push a local edit, send a `sync` envelope containing the Automerge change
   set. The server commits it, replies with an `ack`, and fans it out to every
   other subscriber as a `sync` message.

A complete browser client is in [`examples/notes-app`](../examples/notes-app);
the envelope-by-envelope reference is in
[api-reference/websocket.md](api-reference/websocket.md).

### Try two tabs

Open the notes-app example in two browser tabs pointed at the same server and
`doc_id`. Type in one — the text appears in the other in real time. Disconnect
one tab (go offline), keep editing in the other, then reconnect: the offline tab
catches up via replay and both tabs converge on the same content with no data
loss. That is the core guarantee OpenSyncCRDT provides.

## 5. Switch storage backends

Nothing above changes when you move to a shared database. To use Postgres,
change two environment variables:

```bash
docker run -p 8080:8080 \
  -e STORAGE_BACKEND=postgres \
  -e STORAGE_URL=postgres://user:pass@host:5432/dbname \
  opensynccrdt/server
```

The schema is created automatically on first run. MySQL works the same way with
`STORAGE_BACKEND=mysql`. See the [configuration reference](configuration.md).

## Next steps

- [Configuration reference](configuration.md) — every environment variable.
- [Authentication](auth.md) — the three auth modes.
- [Webhooks](webhooks.md) — run your own logic on sync events.
- [Deployment](deployment/docker.md) — Docker, Kubernetes, VPS, and PaaS.
- [Wire protocol](protocol.md) — the design behind the sync flow.
