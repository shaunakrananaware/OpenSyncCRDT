# Wire protocol

This page explains the design of OpenSyncCRDT's sync protocol. For the exact
message shapes, see the [WebSocket API reference](api-reference/websocket.md).

## Overview

OpenSyncCRDT has two transports:

- **WebSocket** (`/sync`) — the primary, real-time sync channel. Clients connect
  once and stay connected.
- **REST** (`/api/v1/`) — a secondary management API for document CRUD, history,
  snapshots, export, metrics, and cluster info. See the
  [REST reference](api-reference/rest.md).

## The CRDT model

All conflict resolution happens inside [Automerge](https://automerge.org/). The
server treats Automerge change sets as **opaque binary blobs**: it stores them in
order, assigns each a monotonic sequence number per document, and routes them
between devices. It never inspects or interprets document content. The CRDT math
— merging concurrent edits with no data loss — happens in the Automerge library
on each client and on the server-side document copy.

This is why the server needs no schema for your data and why any Automerge
document model (maps, lists, text, counters, booleans) just works.

## Sequence numbers and ordering

Every committed operation on a document gets a sequence number (`seq`) that
increases by one. Sequence numbers give clients a cursor: a client that has
applied up to `seq = 42` can reconnect and ask for "everything after 42",
receiving exactly the operations it missed, in order. This is what makes offline
catch-up reliable.

## Snapshots

Replaying an entire operation log on every startup would be slow for long-lived
documents. The server periodically saves a **snapshot** of the full Automerge
binary state (every `STORAGE_SNAPSHOT_INTERVAL` committed ops, default 100). On
load it restores the latest snapshot and replays only the operations after it.
You can also trigger a snapshot manually via
`POST /api/v1/docs/{id}/snapshot`.

## The sync flow

```
Client A                         Server                         Client B
   |  subscribe(doc, last_seq)      |                              |
   |------------------------------->|                              |
   |     replay(missed ops)         |                              |
   |<-------------------------------|                              |
   |                                |   subscribe(doc, last_seq)   |
   |                                |<-----------------------------|
   |                                |      replay(missed ops)      |
   |                                |----------------------------->|
   |  sync(change set)              |                              |
   |------------------------------->|                              |
   |            ack(seq)            |                              |
   |<-------------------------------|      sync(seq, change set)   |
   |                                |----------------------------->|
```

1. **Subscribe.** A client sends `subscribe` with its `doc_id`, a `session_id`
   it generates, and the highest `last_seq` it already has (`0` for a fresh
   client). The server registers the subscription and streams back every missed
   operation as one or more `replay` batches (large histories are split across
   batches; the final batch has `done: true`).

2. **Push a change.** To commit a local edit, the client sends a `sync` envelope
   whose `payload` is an Automerge binary change set. The server applies it to
   the server-side document, persists it, assigns a `seq`, and replies with an
   `ack`.

3. **Fan-out.** The server broadcasts the committed change to every *other*
   subscriber of that document as a `sync` message (the originating session is
   skipped to avoid echo). In a cluster, the change is also published on Redis so
   nodes serving the same document deliver it to their local subscribers.

4. **Keepalive.** The server sends periodic WebSocket control pings
   (`PING_INTERVAL`); a client that misses the pong window (`PONG_TIMEOUT`) is
   disconnected. Clients may also send an application-level `ping` envelope and
   get a `pong` back.

A client that submits `sync` without a prior `subscribe` is auto-subscribed to
that document (so echo-suppression still works), but explicit `subscribe` is
recommended so you receive catch-up.

## Payload encoding

Automerge change sets and document state are raw bytes. In the default JSON
codec they travel base64-encoded inside the `payload` (or `state`) field — Go's
`encoding/json` marshals and unmarshals `[]byte` as base64 automatically, and
browser clients should base64-encode/decode the `Uint8Array` change sets they
get from Automerge.

## Codec negotiation

The wire codec is negotiated once, at connection time, via the
`Sec-WebSocket-Protocol` header:

- `opensync-json` — the default, human-debuggable JSON codec.
- `opensync-msgpack` — reserved for a future binary codec.

> **Current status:** only the JSON codec is implemented. A client that offers
> `opensync-msgpack` (or offers nothing) still gets a working JSON connection —
> the server transparently falls back to JSON. Don't rely on MessagePack framing
> yet.

## Error handling

When the server can't process a client message it replies with an `error`
envelope carrying a `code` and human-readable `message`. Codes include
`bad_message`, `unauthorized`, `not_subscribed`, `apply_failed`, and
`internal`. A failed operation also fires the
[`on_sync_error`](webhooks.md) webhook. Errors are per-message and do not close
the connection unless they represent a protocol violation (for example, a client
too slow to drain its queue is disconnected).
