# Authentication

Auth is the **developer's responsibility**. OpenSyncCRDT does not manage users,
sessions, or tokens, and it has no concept of document permissions. Instead it
offers three connection-authentication modes that you select with the
`AUTH_MODE` environment variable. Switching modes is a config change and a
restart — never a code change.

> OpenSyncCRDT intentionally does **not** implement user registration/login, JWT
> issuance or validation, or access-control lists. Those live in your
> application. See [what's out of scope](../CONTRIBUTING.md#scope).

## Mode: `none` (default)

```bash
AUTH_MODE=none
```

All connections are accepted. Use this for local development, trusted internal
networks, or when auth is enforced at the network layer (VPN, firewall, private
mesh). This is the default when `AUTH_MODE` is unset.

## Mode: `header`

```bash
AUTH_MODE=header
AUTH_HEADER_NAME=X-User-ID   # default; configurable
```

You run your own auth in front of OpenSyncCRDT — Nginx, an API gateway, or your
own server acting as a proxy. That upstream validates the user and injects a
trusted header before proxying the WebSocket connection.

- If the configured header is **absent**, the connection is rejected with `401`.
- If **present**, its value is taken as the user identity and used in logging and
  webhook payloads (`user_id`).

OpenSyncCRDT **trusts this header completely** and never validates its value. It
is your responsibility to ensure only your trusted upstream can set it — for
example, strip the header from all inbound client requests at your proxy so a
client cannot spoof it.

## Mode: `webhook`

```bash
AUTH_MODE=webhook
AUTH_WEBHOOK_URL=https://yourapp.com/verify-sync-connection
AUTH_WEBHOOK_SECRET=signing-secret
AUTH_WEBHOOK_TIMEOUT=3s        # default
AUTH_WEBHOOK_CACHE_TTL=60s     # default; 0 disables caching
```

On every new WebSocket connection, before accepting it, OpenSyncCRDT calls your
endpoint to authorize the connection.

### Where the token comes from

The `token` in the request is taken from, in order:

1. the `Authorization` header — `Bearer <token>` (the `Bearer ` prefix is
   stripped) or a raw value, else
2. the `token` query parameter (`ws://host/sync?token=...&doc_id=...`).

The `doc_id` is read from the `doc_id` query parameter.

### Request

```
POST https://yourapp.com/verify-sync-connection
Content-Type: application/json
X-OpenSyncCRDT-Signature: <hex HMAC-SHA256 of the body, keyed by AUTH_WEBHOOK_SECRET>

{
  "token": "value from Authorization header or ?token=",
  "doc_id": "document being accessed",
  "action": "connect",
  "timestamp": "2026-07-11T12:00:00Z"
}
```

Verify the signature before trusting the body — see
[verifying signatures](webhooks.md#verifying-signatures) (the scheme is
identical to the event webhooks).

### Response

Return `200` to allow, any other status to deny:

```json
{
  "allowed": true,
  "user_id": "alice",
  "metadata": {}
}
```

- `user_id` is used as the identity in event payloads.
- `metadata` is an arbitrary object carried alongside the identity.
- A non-`200` response, a body with `"allowed": false`, a transport error, or a
  timeout all result in the connection being **rejected**.

### Caching

To avoid hammering your auth server during reconnect storms, allow/deny
decisions are cached **per token per `doc_id`** for `AUTH_WEBHOOK_CACHE_TTL`
(default 60s). Set it to `0` to disable caching and call your endpoint on every
connection.

## Choosing a mode

| Situation | Mode |
| --------- | ---- |
| Local dev, trusted network, network-level auth | `none` |
| You already have a proxy/gateway that authenticates users | `header` |
| You want OpenSyncCRDT to ask your app to authorize each connection | `webhook` |

## Transport security

Authentication is separate from transport encryption. For encrypted transport,
either enable TLS in the binary (`TLS_ENABLED=true` with cert/key) or terminate
TLS at a load balancer / reverse proxy in front of it and use `wss://`.
End-to-end encryption is out of scope — TLS is the transport security boundary.
