# WebSocket API reference

All real-time sync happens over a single WebSocket endpoint.

- Endpoint: `ws://host:port/sync` (or `wss://` behind TLS)
- Subprotocol: `opensync-json` (default and only implemented codec)

For the design and flow behind these messages, see the
[protocol overview](../protocol.md).

## Connecting

```
GET /sync HTTP/1.1
Upgrade: websocket
Sec-WebSocket-Protocol: opensync-json
```

Requesting the subprotocol is optional — a bare client still gets a JSON
connection. The `opensync-msgpack` subprotocol is reserved but **not yet
implemented**; offering it transparently falls back to JSON.

Authentication happens during the upgrade, per `AUTH_MODE`:

- `header` mode reads the configured identity header (missing ⇒ `401`).
- `webhook` mode reads the token from the `Authorization` header or `?token=`
  query param, and `doc_id` from `?doc_id=`, then calls your endpoint (deny ⇒
  `401`).

See [Authentication](../auth.md).

## Envelopes

Every message is a JSON object discriminated by `type`. Binary payloads
(Automerge change sets and state) are base64-encoded strings in JSON.

### Client → server

**`subscribe`** — join a document and request catch-up of everything after
`last_seq`:

```json
{ "type": "subscribe", "doc_id": "my-doc", "session_id": "client-generated", "last_seq": 42 }
```

Use `last_seq: 0` for a fresh client. `doc_id` is required.

**`sync`** — submit a local Automerge change set:

```json
{ "type": "sync", "doc_id": "my-doc", "session_id": "client-generated", "payload": "<base64 change set>" }
```

The server applies and persists it, replies with an `ack`, and fans it out to
other subscribers. A `sync` without a prior `subscribe` auto-subscribes the
connection to that document.

**`ping`** — application-level keepalive:

```json
{ "type": "ping" }
```

### Server → client

**`sync`** — a peer's committed change, fanned out to you (never an echo of your
own):

```json
{ "type": "sync", "doc_id": "my-doc", "from_session": "other-client", "seq": 47, "payload": "<base64 change set>" }
```

**`replay`** — a batch of missed operations after a `subscribe`. Large histories
span multiple batches; the last has `done: true`:

```json
{
  "type": "replay",
  "doc_id": "my-doc",
  "ops": [
    { "seq": 43, "from_session": "abc", "payload": "<base64>" },
    { "seq": 44, "from_session": "def", "payload": "<base64>" }
  ],
  "batch_seq": 1,
  "total_batches": 3,
  "done": false
}
```

**`ack`** — confirms your `sync` was committed at the given sequence:

```json
{ "type": "ack", "seq": 47 }
```

**`error`** — a problem processing a client message:

```json
{ "type": "error", "code": "apply_failed", "message": "..." }
```

Codes: `bad_message`, `unauthorized`, `not_subscribed`, `apply_failed`,
`internal`. An error is per-message and usually does not close the connection.

**`pong`** — reply to a client `ping`:

```json
{ "type": "pong" }
```

## Keepalive and limits

- The server sends WebSocket **control pings** every `PING_INTERVAL` (default
  30s); a client that doesn't pong within `PONG_TIMEOUT` (default 10s) is
  disconnected. This is separate from the application-level `ping`/`pong`
  envelopes above.
- Inbound messages larger than `MAX_MESSAGE_SIZE_BYTES` (default 1 MiB) are
  rejected.
- A client too slow to drain the server's outbound queue is disconnected to
  protect the engine.

See [configuration](../configuration.md#connection-limits).

## Applying payloads on the client

`payload` fields carry raw Automerge change sets; `state` (from the REST export)
carries a full document. With `@automerge/automerge`:

```js
doc = Automerge.loadIncremental(doc, base64ToBytes(msg.payload)); // a sync/replay op
doc = Automerge.load(base64ToBytes(exportedState));               // a full export
```

To produce a change set to send, use `Automerge.getLastLocalChange(doc)` after
`Automerge.change(...)` and base64-encode the resulting `Uint8Array`. A complete
example is in [`examples/notes-app`](../../examples/notes-app).
