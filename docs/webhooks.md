# Webhooks

Webhooks let you run your own business logic when things happen in the sync
engine — without modifying the binary. They are outbound HTTP `POST` calls the
server makes to URLs you configure.

There are two families:

- **Event webhooks** (this page) — fire-and-forget notifications of sync events.
- **Blocking webhooks** — the [auth webhook](auth.md#mode-webhook) and the
  [conflict-resolver webhook](#conflict-resolver-webhook), which the server waits
  on because their responses change what it does next.

## Event webhooks

All event webhooks are optional. An event with no configured URL is silently
skipped. Configure them individually, or point several at the same URL and
switch on the `X-OpenSyncCRDT-Event` header.

```bash
WEBHOOK_SECRET=shared-signing-secret
WEBHOOK_TIMEOUT=5s            # default
WEBHOOK_MAX_RETRIES=3         # default
WEBHOOK_ON_DOCUMENT_CREATED_URL=https://yourapp.com/hooks
WEBHOOK_ON_DOCUMENT_UPDATED_URL=https://yourapp.com/hooks
WEBHOOK_ON_DOCUMENT_DELETED_URL=https://yourapp.com/hooks
WEBHOOK_ON_CLIENT_CONNECTED_URL=https://yourapp.com/hooks
WEBHOOK_ON_CLIENT_DISCONNECTED_URL=https://yourapp.com/hooks
WEBHOOK_ON_SYNC_ERROR_URL=https://yourapp.com/hooks
```

### Request format

Every event webhook is a `POST` with a JSON body and these headers:

| Header | Value |
| ------ | ----- |
| `Content-Type` | `application/json` |
| `X-OpenSyncCRDT-Event` | the event name, e.g. `on_document_updated` |
| `X-OpenSyncCRDT-Signature` | hex HMAC-SHA256 of the raw body, keyed by `WEBHOOK_SECRET` |
| `X-OpenSyncCRDT-Timestamp` | Unix timestamp (seconds) when the delivery was created |

Delivery is **fire-and-forget** (non-blocking): your response body is ignored
and the sync engine does not wait for it. A delivery is considered successful on
any `2xx` status. Non-`2xx` responses, timeouts, and transport errors are
retried up to `WEBHOOK_MAX_RETRIES` times (so `WEBHOOK_MAX_RETRIES + 1` attempts
total) with exponential backoff starting at 200 ms. Exhausted retries are
logged and counted in metrics but never crash the server.

### Events and payloads

Every payload includes the `event` name and an ISO-8601 `timestamp`.

**`on_document_created`** — a new document is first written to:

```json
{ "event": "on_document_created", "doc_id": "...", "user_id": "alice-or-null", "timestamp": "..." }
```

**`on_document_updated`** — after every committed operation:

```json
{ "event": "on_document_updated", "doc_id": "...", "seq": 47, "user_id": "...", "session_id": "...", "timestamp": "..." }
```

**`on_document_deleted`** — a document is deleted via the management API:

```json
{ "event": "on_document_deleted", "doc_id": "...", "user_id": "...", "timestamp": "..." }
```

**`on_client_connected`** — a device opens a connection and subscribes to a doc:

```json
{ "event": "on_client_connected", "doc_id": "...", "session_id": "...", "user_id": "...", "remote_addr": "1.2.3.4:5678", "timestamp": "..." }
```

**`on_client_disconnected`** — a device's connection closes (fired per
subscribed document):

```json
{ "event": "on_client_disconnected", "doc_id": "...", "session_id": "...", "user_id": "...", "duration_seconds": 42, "timestamp": "..." }
```

**`on_sync_error`** — an operation fails to apply, or a conflict-resolver webhook
fails:

```json
{ "event": "on_sync_error", "doc_id": "...", "session_id": "...", "error_code": "apply_failed", "error_message": "...", "timestamp": "..." }
```

`user_id` is `null` when there is no identity (e.g. `AUTH_MODE=none`).

### Verifying signatures

The signature is `hex(HMAC-SHA256(WEBHOOK_SECRET, raw_request_body))`. Compute it
over the **exact bytes** you received (before any JSON re-encoding) and compare
in constant time. Example in Go:

```go
func valid(secret string, body []byte, sigHeader string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(sigHeader))
}
```

Example in Node.js:

```js
import crypto from "node:crypto";
function valid(secret, rawBody, sigHeader) {
  const expected = crypto.createHmac("sha256", secret).update(rawBody).digest("hex");
  const a = Buffer.from(expected), b = Buffer.from(sigHeader);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}
```

The same signing scheme is used by the auth and conflict-resolver webhooks (each
with its own secret).

## Conflict-resolver webhook

By default, Automerge resolves concurrent changes automatically with no data
loss — you don't need this webhook. Configure it only when you want to override
resolution with your own logic.

```bash
CONFLICT_RESOLVER_URL=https://yourapp.com/resolve-conflict
CONFLICT_RESOLVER_SECRET=webhook-signing-secret
CONFLICT_RESOLVER_TIMEOUT=5s   # default
```

When a conflict is detected the server posts both versions and the current state
to your endpoint (signed with `X-OpenSyncCRDT-Signature` under
`CONFLICT_RESOLVER_SECRET`):

```json
{
  "doc_id": "...",
  "change_a": "base64 automerge change set",
  "change_b": "base64 automerge change set",
  "current_state": "base64 automerge doc state",
  "timestamp": "..."
}
```

Your endpoint returns the resolved state:

```json
{ "resolved_state": "base64 automerge doc state" }
```

The server commits and broadcasts that state. If the webhook **times out or
returns an error**, the server falls back to automatic Automerge resolution,
logs a warning, and fires [`on_sync_error`](#events-and-payloads). Unlike the
event webhooks, this call is **blocking** — the operation waits for the resolved
state (or the timeout).
