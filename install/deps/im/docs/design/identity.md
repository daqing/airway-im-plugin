# Identity and Airway-Application Authentication Design

## 1. Purpose and scope

This plugin is a generic IM backend embedded into other projects (mini
programs, mobile apps, websites). Airway applications already run their own
account systems, so the plugin never manages passwords, OAuth flows, or
sessions of its own. Instead, a user is identified by the pair
`(name, uuid)` assigned by the Airway project, carried inside a signed credential
that the plugin verifies statelessly.

This document defines:

- the trust model between the Airway backend, the client, and the IM
  backend;
- the credential format and signing algorithm, with reference
  implementations;
- how clients present the credential over HTTP and WebSocket;
- how users are registered and refreshed in the `users` table; and
- rotation, revocation, and operational limits.

## 2. Trust model

Clients (browsers, mini programs, apps) are untrusted: a client must never
assert its own `(name, uuid)` in plain text, because anyone could then
impersonate any user. Identity is established through a shared-secret
signature:

```text
Airway backend (trusted)              IM backend (trusted)
  holds IM_AUTH_SECRET  ── shared ──  holds IM_AUTH_SECRET
        │                                    ▲
        │ signs (name, uuid)                 │ verifies signature
        ▼                                    │
     client (untrusted) ── presents credential over HTTP / WebSocket
```

- `IM_AUTH_SECRET` never leaves the server side: the Airway backend knows
  it, and third-party platform backends integrating through the minting
  API hold only `IM_INTERNAL_SECRET` instead (§4.1).
- The client receives an opaque credential and presents it; it cannot
  forge or alter one.
- The IM backend needs no callback to the Airway project to authenticate a request:
  verification is local HMAC comparison plus a time-window check.
- One IM deployment can serve one or several Airway applications; all of
  them sign with the same secret. `uuid` values must therefore be unique
  across every integrated Airway project (prefixing, e.g. `shop1:user-42`, is a
  simple way to guarantee this).

## 3. Credential format

```text
im1.<base64url(payload JSON)>.<base64url(HMAC-SHA256(secret, "im1." + payload))>
```

- `payload` is the base64url-encoded (no padding) JSON claims document.
- The signature is base64url-encoded (no padding) HMAC-SHA256 over the
  literal string `im1.` concatenated with the encoded payload — i.e. the
  first two segments of the credential, joined by the dot.
- The `im1` prefix versions the format; verifiers reject anything else.

### Claims

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `uuid` | string, 1–64 chars | yes | Stable, unique identity of the user in the Airway system. Never reused across users. |
| `name` | string, 1–64 chars | yes | Username recorded in the IM `users` table; refreshed on every login when it changes. |
| `nickname` | string, ≤ 64 chars | no | Display name; stored only when present. |
| `avatar_url` | string, ≤ 2048 chars | no | Avatar; stored only when present. |
| `token_version` | integer ≥ 1 | no | Revocation counter. Credentials minted by `POST /internal/v1/credentials` always carry it; verification rejects a credential whose version differs from `users.token_version` (see §9). Absent (or 0) disables the check. |
| `iat` | unix seconds | yes | Issued-at time. Credentials from the future (beyond a 1-minute clock-skew tolerance) are rejected. |
| `exp` | unix seconds | recommended | Expiry. Omit only for long-lived service credentials; see §9. |

Optional display fields are deliberately "write-only-when-present": a
minimal credential carrying just `(uuid, name)` never clobbers a richer
profile stored earlier.

## 4. Minting credentials

There are two supported ways to produce a credential. Both yield the same
format; §4.1 explains which path fits which deployment topology.

### 4.1 Who signs: a server, never a client

Four roles appear in a deployment:

1. **The third-party platform** — its own front end (a mini program or
   browser JS) and its own back end (most often PHP or Java);
2. **The Airway project** — a Go application (`airway new`) with this
   plugin installed (`airway plugin:install`), deployed as an
   independent microservice that provides the IM service;
3. **The plugin** — embedded in the Airway project, providing the IM API,
   gateway, and delivery services;
4. **The end user**, who logs in on the third-party platform.

The Airway project and the platform integrate purely over HTTP: a PHP or
Java backend cannot embed Go code, so the Airway project exposes APIs and the
platform's backend calls them. The credential is minted server-side and
handed to the client by its own platform:

1. The end user logs in through the platform's own existing login
   (password, SMS code, `wx.login`/code2session, …). That login flow is
   the client authentication for the whole system; the plugin adds none
   and needs none.
2. After the login succeeds, the platform's backend reads `(name, uuid)`
   from its own user table and obtains the credential
   **server-to-server**: it calls the Airway project's minting endpoint
   `POST /internal/v1/credentials` with `IM_INTERNAL_SECRET` (§4.3).
   Platform backends are never given the signing secret, and plaintext
   `(name, uuid)` is never accepted from a client.
3. The platform's backend returns the finished credential in its login
   response; the client only carries and presents it (§5). It holds no
   secret: it can neither mint nor alter a credential.

The only path for third-party platform backends is the minting API:
`IM_AUTH_SECRET` never leaves the Airway service, platform backends hold
only `IM_INTERNAL_SECRET`, and minted credentials carry `token_version`,
so each platform's users stay individually revocable (§9.1).

Two hard consequences follow:

- **An integrating platform must have its own server backend.** A pure
  front-end client with no server — a serverless mini program, a static
  page — cannot integrate securely: it has no server of its own to
  authenticate the user and no safe place to keep even
  `IM_INTERNAL_SECRET`; whoever could mint credentials directly could
  impersonate any user.
- **The IM backend never issues credentials to clients.** The public
  API (:1905) only verifies; the minting endpoint lives on the internal
  listener (default `127.0.0.1:1906`, rebindable to a private
  interface, §4.3) and requires `IM_INTERNAL_SECRET`. An attacker can
  send plaintext `(name, uuid)` to the public API from anywhere — there
  is nothing there that accepts it.

"Signed plaintext" is exactly the point: the payload segment is plain
base64 and anyone can decode it (readable), but without the secret no
one can produce a payload whose signature verifies (not forgeable) —
changing one byte invalidates the signature, and the backend recomputes
the HMAC before trusting the claims.

### 4.2 Option A: sign in the Airway backend (recommended for direct embedding)

The Airway backend signs locally with `IM_AUTH_SECRET`. This adds no
network hop to the login flow, and HMAC-SHA256 is a standard primitive
available in every server language (Go, Node.js, Python, PHP, Java, …)
— no Go code is embedded anywhere. This option is reserved for the
Airway project's own backend: third-party platform backends in a resale
topology always integrate through the minting API (§4.3) and are never
given the signing secret.

Go:

```go
func mintCredential(secret, uuid, name string, ttl time.Duration) string {
    payload, _ := json.Marshal(map[string]any{
        "uuid": uuid,
        "name": name,
        "iat":  time.Now().Unix(),
        "exp":  time.Now().Add(ttl).Unix(),
    })
    encoded := base64.RawURLEncoding.EncodeToString(payload)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte("im1." + encoded))
    return "im1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
```

Node.js:

```js
const crypto = require("crypto");

function mintCredential(secret, uuid, name, ttlSeconds) {
  const now = Math.floor(Date.now() / 1000);
  const payload = Buffer.from(
    JSON.stringify({ uuid, name, iat: now, exp: now + ttlSeconds })
  ).toString("base64url");
  const sig = crypto
    .createHmac("sha256", secret)
    .update("im1." + payload)
    .digest("base64url");
  return `im1.${payload}.${sig}`;
}
```

Python:

```python
import base64, hashlib, hmac, json, time

def mint_credential(secret: str, uuid: str, name: str, ttl_seconds: int) -> str:
    now = int(time.time())
    payload = base64.urlsafe_b64encode(
        json.dumps({"uuid": uuid, "name": name, "iat": now, "exp": now + ttl_seconds},
                   separators=(",", ":")).encode()
    ).rstrip(b"=").decode()
    sig = base64.urlsafe_b64encode(
        hmac.new(secret.encode(), f"im1.{payload}".encode(), hashlib.sha256).digest()
    ).rstrip(b"=").decode()
    return f"im1.{payload}.{sig}"
```

Ruby:

```ruby
require "base64"
require "json"
require "openssl"

def mint_credential(secret, uuid, name, ttl_seconds)
  now = Time.now.to_i
  payload = Base64.urlsafe_encode64(
    JSON.generate({ uuid: uuid, name: name, iat: now, exp: now + ttl_seconds }),
    padding: false
  )
  signature = Base64.urlsafe_encode64(
    OpenSSL::HMAC.digest("sha256", secret, "im1.#{payload}"),
    padding: false
  )
  "im1.#{payload}.#{signature}"
end
```

The `airway-im-sdk-ruby` gem wraps this as `AirwayIM::Credentials.sign`
(plus decode/verify helpers and the whole IM API).

The Airway project typically mints a credential when its own session is established
(login, token refresh) and returns it to the client alongside its own
session token.

### 4.3 Option B: the IM backend mints on the Airway project's behalf

Backends that should not hold the signing secret — third-party platform
backends (PHP, Java, …) in a resale topology, or the Airway project's own
non-Go services — call the server-to-server endpoint during their own
login flow:

```http
POST /internal/v1/credentials
X-IM-Internal-Secret: <IM_INTERNAL_SECRET>
Content-Type: application/json

{"uuid": "user-42", "name": "alice", "nickname": "Alice", "ttl_seconds": 86400}
```

Response:

```json
{"code": 0, "data": {"credential": "im1.…", "expires_at": "2026-09-15T08:00:00Z"}, "message": null}
```

- `uuid` and `name` are required (1–64 characters each);
  `nickname`/`avatar_url` are optional.
- `ttl_seconds` defaults to 86400 (24 h) and is capped at 2592000
  (30 days). `0` omits the expiry claim — avoid this for end users.
- The user is registered (or refreshed) at mint time, and the credential
  carries the user's current `token_version`, making it revocable through
  the admin API (§9).
- The endpoint lives on the internal API, which the backend serves on its
  own listener (`IM_INTERNAL_ADDR`, default `127.0.0.1:1906`) — separate
  from the public HTTP port — and every request must still carry
  `IM_INTERNAL_SECRET`. Callers on other machines (a third-party
  platform's PHP/Java backend) must share a private network with the
  Airway project — same VPC or data-center intranet, or a VPN / private
  line — in which case rebind `IM_INTERNAL_ADDR` to that private
  interface and callers reach it by its private address. Keep the
  listener off the public network; it must never be reachable by clients.

## 5. Client usage

### 5.1 HTTP API

Every IM API request carries the credential as a bearer token:

```http
Authorization: Bearer im1.<payload>.<signature>
```

Invalid, expired, or missing credentials are rejected with envelope code
`10001`.

### 5.2 WebSocket

Clients connect to the gateway (`/ws`) and authenticate with the **first**
application message, within 10 seconds of the upgrade:

```json
{"cmd": "auth", "opts": ["im1.<payload>.<signature>"]}
```

The gateway forwards the credential to the backend's
`GET /internal/v1/auth`, which verifies it and returns the internal
`user_id`. On success the connection replies
`{"code":0,"data":"OK","message":null}` and is bound to the user; on
failure or timeout it is closed with code `1008`. See
[`gateway.md`](gateway.md) for the full wire protocol.

### 5.3 Expiry and reconnection

A WebSocket connection, once authenticated, stays valid until it drops —
`exp` is only evaluated at authentication time. Clients must therefore
**fetch a fresh credential from their platform's backend before
reconnecting** when the old one has expired. Airway projects should pick a TTL
that matches their own session lifetime (the 24 h default suits most
apps).

## 6. Registration and profile refresh

Every successful verification resolves the claims onto the `users` table
(`app/auth/auth.go`):

1. **First sight** — no row with that `uuid` exists: a row is inserted
   with `uuid`, `username = name`, optional display fields, and
   `last_seen_at = now`. Concurrent first logins race on the `UNIQUE(uuid)`
   constraint; the loser reads back the winning row, so exactly one user
   is ever created per `uuid`.
2. **Returning user** — `username` is updated whenever `name` changed;
   `nickname`/`avatar_url` are updated only when the credential carries
   them; `last_seen_at` is refreshed at most once per 5 minutes so that
   authenticating every HTTP request does not become a write on every
   request.

Client APIs reference conversation members by the Airway project's `uuid`
(conversation creation, member management); internally the numeric `users.id`
still links `conversation_members` rows and routes delivery. The `uuid` never
appears in delivery events or inter-service traffic.

## 7. Admin visibility

Because registration is automatic, the admin API doubles as the integration
dashboard:

- `GET /admin/api/users` lists every registered identity with
  `uuid`, `username`, display fields, `token_version`, `created_at`, and
  `last_seen_at` (5-minute granularity).
- `POST /admin/api/users/{uuid}/revoke` invalidates a user's outstanding
  credentials and kicks their live connections (§9).
- `GET /admin/api/status` aggregates user counts with gateway and
  delivery metrics, plus the online user IDs reported by the gateway's
  `GET /internal/v1/online`. With multiple gateway instances, merge the
  per-instance lists; `last_seen_at` remains the durable fallback for
  "recently active".

## 8. Secret rotation

Verification accepts two environment variables, read on every call so
rotation takes effect without a restart:

- `IM_AUTH_SECRET` — the current signing secret;
- `IM_AUTH_SECRET_PREVIOUS` — the previous secret, accepted during
  rotation.

Rotation procedure:

1. Set `IM_AUTH_SECRET_PREVIOUS` to the old secret and `IM_AUTH_SECRET` to
   the new one on the IM backend.
2. Switch Airway backends to sign with the new secret.
3. Once every credential signed with the old secret has expired (at most
   the maximum TTL in use), remove `IM_AUTH_SECRET_PREVIOUS`.

## 9. Revocation and security limits

### 9.1 Per-user revocation

Every user row carries a `token_version` counter (starting at 1).
Credentials minted by the backend (§4.3) sign the current value into the
`token_version` claim; verification rejects any credential whose version
no longer matches the row. Revoking a user is one admin call:

```http
POST /admin/api/users/{uuid}/revoke
Authorization: Bearer <admin session token>
```

```json
{"code": 0, "data": {"uuid": "user-42", "token_version": 2, "connections_kicked": 1}, "message": null}
```

The call increments `token_version` — immediately invalidating every
outstanding versioned credential for the user — and best-effort kicks the
user's live connections through the gateway (`POST
{IM_GATEWAY_URL}/internal/v1/kick`, close code 1008). Unknown `uuid`
returns 404.

Semantics and limits:

- Revocation invalidates credentials; it does not ban the user. The Airway project
  can mint a fresh credential (carrying the new version) at any time — to
  keep a user out, the Airway project must stop minting for them.
- Airway-minted credentials that omit the `token_version` claim (§4.2) are
  unaffected. Deployments that need every client revocable should route
  minting through `/internal/v1/credentials`, or have the Airway project track and
  sign the version itself.
- With multiple gateway instances the kick reaches only the instance
  named by `IM_GATEWAY_URL`; connections elsewhere end at their next
  reconnect, when the revoked credential fails authentication.

### 9.2 Credential hygiene

- Keep TTLs short (hours, not days) for end-user credentials: a leaked
  unversioned credential stays valid until `exp`.
- Treat `0`-TTL (no expiry) credentials as service credentials and guard
  them accordingly.
- Rotate `IM_AUTH_SECRET` (§8) to invalidate **all** outstanding
  credentials at once — the emergency brake if the secret itself leaks.

### 9.3 Trust limits

Further limits, by design:

- the plugin trusts whatever `(name, uuid)` the Airway project signs — enforcing
  that an Airway user may only obtain credentials for themselves is the
  Airway project's responsibility;
- the credential is a bearer instrument: serve everything over TLS, and
  restrict browser clients with `GATEWAY_ALLOWED_ORIGINS`;
- credentials never appear in logs, delivery events, metrics, or API
  responses other than the minting response itself.

## 10. Error codes

| Code | Where | Meaning |
| --- | --- | --- |
| `10001` | client APIs, `/internal/v1/auth` | Credential missing, malformed, bad signature, or expired. |
| `10002` | gateway | First WebSocket message was not a valid `auth` command. |
| `10003` | `/internal/v1/credentials` | Invalid request body (missing/oversized fields, bad TTL). |
| `10005` | internal APIs | `X-IM-Internal-Secret` missing or wrong. |
| `10006` | `/internal/v1/credentials` | `IM_AUTH_SECRET` not configured on the backend. |
