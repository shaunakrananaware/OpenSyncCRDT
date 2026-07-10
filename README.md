# OpenSyncCRDT

OpenSyncCRDT is an open-source, production-grade, local-first sync engine that
compiles to a single Go binary and gives any app reliable multi-device,
offline-capable sync without you having to learn distributed systems.

## The problem it solves

Adding sync to a local-first app (notes, todos, collaborative editors) normally
means stitching together a CRDT library, a WebSocket server, a persistence
layer, conflict resolution, and a multi-service deployment — months of
distributed-systems work before you ship a single feature. OpenSyncCRDT
collapses all of that into one binary: it stores and orders
[Automerge](https://automerge.org/) change sets, routes them between devices in
real time, resolves conflicts with no data loss, and survives restarts. Auth,
access control, and business logic stay yours; OpenSyncCRDT handles only the
sync infrastructure.

## Quickstart

```bash
docker run -p 8080:8080 opensynccrdt/server
```

That's a working sync server: a WebSocket sync endpoint at `ws://localhost:8080/sync`,
a REST management API under `http://localhost:8080/api/v1/`, and Kubernetes
`/health` / `/ready` probes. It uses the embedded SQLite backend by default —
add `-v ./data:/data` to persist across restarts.

Verify it's up:

```bash
curl localhost:8080/health   # {"status":"ok"}
curl localhost:8080/ready    # {"status":"ready"}
```

## Minimal integration example

Sync is a WebSocket connection to `/sync` carrying Automerge change sets as
base64 inside JSON envelopes. A browser client using
[`@automerge/automerge`](https://www.npmjs.com/package/@automerge/automerge):

```js
import * as Automerge from "@automerge/automerge";

const sessionId = crypto.randomUUID();
const docId = "my-doc";
let doc = Automerge.init();

const ws = new WebSocket("ws://localhost:8080/sync", "opensync-json");

ws.onopen = () => {
  // Join the document and ask for everything we've missed (from seq 0).
  ws.send(JSON.stringify({ type: "subscribe", doc_id: docId, session_id: sessionId, last_seq: 0 }));
};

ws.onmessage = (evt) => {
  const msg = JSON.parse(evt.data);
  if (msg.type === "sync") {
    doc = Automerge.loadIncremental(doc, base64ToBytes(msg.payload));
  } else if (msg.type === "replay") {
    for (const op of msg.ops) doc = Automerge.loadIncremental(doc, base64ToBytes(op.payload));
  }
};

// Make a local change and push it to the server as a binary change set.
function edit(mutator) {
  const before = doc;
  doc = Automerge.change(doc, mutator);
  const change = Automerge.getLastLocalChange(doc); // Uint8Array
  ws.send(JSON.stringify({
    type: "sync", doc_id: docId, session_id: sessionId, payload: bytesToBase64(change),
  }));
}
```

A complete, runnable version lives in [`examples/notes-app`](examples/notes-app).
The full envelope reference is in
[docs/api-reference/websocket.md](docs/api-reference/websocket.md).

## Documentation

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Wire protocol](docs/protocol.md)
- [Authentication](docs/auth.md)
- [Webhooks](docs/webhooks.md)
- API reference: [REST](docs/api-reference/rest.md) · [WebSocket](docs/api-reference/websocket.md)
- Contributing: [development setup](docs/contributing/development-setup.md) · [architecture](docs/contributing/architecture.md)

## Deployment options

OpenSyncCRDT is a single static binary, so it runs almost anywhere:

- **Docker** — three image variants (`latest`, `alpine`, `distroless`). See [docs/deployment/docker.md](docs/deployment/docker.md).
- **Kubernetes** — manifests in [`deploy/kubernetes/`](deploy/kubernetes) and a Helm chart in [`deploy/helm/`](deploy/helm). See [docs/deployment/kubernetes.md](docs/deployment/kubernetes.md).
- **VPS / bare metal** — systemd unit and `install.sh` in [`deploy/`](deploy). See [docs/deployment/vps.md](docs/deployment/vps.md).
- **PaaS one-click** — [Fly.io](docs/deployment/fly.md), [Railway](docs/deployment/railway.md), Render, and Coolify configs under [`deploy/`](deploy).
- **Embedded** — import `github.com/shaunakrananaware/OpenSyncCRDT/pkg/engine` and mount the handler on your own Go mux. See [`examples/embedded-server`](examples/embedded-server).

Scale out with `CLUSTER_MODE=true` and Redis; multiple nodes share one
Postgres/MySQL database and fan out operations over Redis pub/sub. Cluster
deployments require sticky-session load balancing — see
[docs/deployment/kubernetes.md](docs/deployment/kubernetes.md).

**Supported platforms:** `linux/amd64`, `linux/arm64`, `darwin/amd64`,
`darwin/arm64`. Windows is not supported — the mandated Automerge CRDT core
ships prebuilt native archives only for those four platforms.

## Configuration

Everything is configured by environment variable or an optional YAML file — no
code changes. See the full [configuration reference](docs/configuration.md) and
the annotated [`config.example.yaml`](config.example.yaml). Switching storage
from SQLite to Postgres, for example, is two environment variables:

```bash
-e STORAGE_BACKEND=postgres -e STORAGE_URL=postgres://user:pass@host:5432/dbname
```

## Contributing

Contributions are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md) for
environment setup, how to run the tests, and the PR process. All participants
are expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a
security issue, see [SECURITY.md](SECURITY.md).

## License

OpenSyncCRDT is licensed under the [Apache License 2.0](LICENSE).
