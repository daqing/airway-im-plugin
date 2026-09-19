# airway-im-plugin

An [Airway](https://github.com/daqing/airway) plugin that packages a
complete IM chat backend: host-signed credential identity, direct
and group conversations, durable messaging with sequence-based
synchronization, an admin API with content moderation, a WebSocket gateway,
and a transactional-outbox delivery worker. The companion WebSocket gateway
and delivery services ship under [`install/deps/`](install/deps/), so the whole stack can run
standalone from any Airway host app that enables the plugin.

The plugin authenticates users through host-signed HMAC credentials: the host
application signs its `(name, uuid)` identity pair, and the plugin verifies
it statelessly.
A Chinese version of this document is available at
[`install/deps/im/docs/README.zh-cn.md`](install/deps/im/docs/README.zh-cn.md).

## Table of contents

- [Architecture](#architecture)
- [Features](#features)
- [Repository layout](#repository-layout)
- [Setup](#setup)
- [Authenticating users](#authenticating-users)
- [Using the IM API](#using-the-im-api)
- [Connecting over WebSocket](#connecting-over-websocket)
- [Configuration reference](#configuration-reference)
- [Development](#development)

## Architecture

The plugin follows a three-service layout — backend / gateway / delivery:

```text
                    HTTPS (REST)                     WebSocket
  Clients ───────────────────────────────► backend :1905
     │                                        ▲
     │  ws://gateway:1910/ws                  │ 2. poll outbox
     ▼                                        │    (events written in step 1's
  gateway :1910 ◄───── 3. deliver + ack ── delivery :1920   transaction)
     │
     └─ 4. fan out to connected recipients
```

1. A client sends a message over HTTPS. The backend validates membership,
   allocates the conversation `sequence`, inserts the message, and writes an
   `outbox_events` row — all in one database transaction.
2. The delivery worker polls the backend's internal outbox API.
3. Each committed event is pushed to the gateway's internal deliver endpoint
   and acknowledged only after the gateway accepted it.
4. The gateway fans the event out to the locally connected recipients.

The backend's internal service-to-service API (`/internal/v1/*` — the outbox
poll/ack in step 2, the gateway's auth check, credential minting) is served
on a separate listener that defaults to loopback only (`IM_INTERNAL_ADDR`,
default `127.0.0.1:1906`); it is never mounted on the public port.

WebSocket delivery is a latency optimization, never the durable copy of a
message: offline clients recover through the sequence-based synchronization
API. Delivery is at-least-once; clients deduplicate by `message_id` /
`event_id` and reorder by `sequence`.

Design contracts:

- [`install/deps/im/docs/design/identity.md`](install/deps/im/docs/design/identity.md) — host-signed
  credential format, minting, client usage, rotation and revocation
- [`install/deps/im/docs/design/gateway.md`](install/deps/im/docs/design/gateway.md) — wire protocol,
  connection lifecycle, limits, security model
- [`install/deps/im/docs/design/delivery.md`](install/deps/im/docs/design/delivery.md) — outbox pattern,
  fan-out strategy, ordering and idempotency semantics
- [`install/deps/im/docs/design/conversation.md`](install/deps/im/docs/design/conversation.md) — conversation
  model, direct-conversation uniqueness, membership and authorization rules

## Features

**Identity**

- The plugin ships no login flow: a host application signs its users'
  `(name, uuid)` pair into an HMAC-SHA256 credential with a shared secret
  (`IM_AUTH_SECRET`), and the plugin verifies it statelessly.
- A generic `users` table (uuid, username, nickname, avatar URL, email,
  last-seen) is the identity source of truth for all IM APIs; rows are
  auto-registered the first time a valid credential authenticates.
- Hosts may sign credentials themselves (any language, no extra dependency)
  or call `POST /internal/v1/credentials` to have the backend mint them.
- `GET /api/v1/me` profile lookup by credential; credentials are never
  exposed in API responses, delivery events, or logs.

**Conversations**

- One model for both kinds: `direct` (exactly two members, normalized-pair
  uniqueness, get-or-create semantics) and `group` (creator becomes `owner`,
  arbitrary member sets, `owner`/`admin`/`member` roles).
- `POST /api/v1/conversations/:uuid/members` adds members to an existing group
  and `DELETE /api/v1/conversations/:uuid/members/:user_id` removes one
  (owner/admin; an admin manages plain members only; the owner cannot be
  removed). Both are idempotent and fan out `conversation.member_added` /
  `conversation.member_removed` events to all active members — a removal also
  notifies the kicked user.
- Opaque 26-character ULID identifiers; membership history preserved via
  `left_at` instead of row deletion.

**Messages**

- `POST /api/v1/messages` and the nested conversation variant with
  `text/markdown` (default) or `text/plain` content, 32 KiB limit.
- Optional `Idempotency-Key` header: the same key + same body replays the
  canonical message; a different body with the same key is a 409.
- Per-conversation monotonic `sequence` allocated transactionally;
  `GET .../messages?after_sequence=N` synchronizes missed messages after a
  reconnect, with gap detection on the client.

**Realtime delivery**

- Standalone WebSocket gateway with the `{"cmd":"auth","opts":["<credential>"]}`
  first-command protocol, 10-second auth deadline, 30s/40s ping/pong, 64 KiB
  frame limit, and 256-message bounded outbound queues (slow consumers are
  disconnected and recover via synchronization).
- Multiple devices per user are supported; one write loop per connection.

**Admin & moderation**

- `/admin/api` with session login (`IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD`),
  12-hour in-memory sessions.
- System status aggregating database counters plus live gateway/delivery
  metrics and online user IDs; user listing with last-seen timestamps.
- Credential revocation: `POST /admin/api/users/:uuid/revoke` bumps the
  user's `token_version` (invalidating backend-minted credentials) and
  kicks live gateway connections.
- Group conversation browser, message viewer, and one-click
  `mark-illegal`: illegal content is masked to `***` for clients and a
  `message.moderated` event fans out to online members.

**Observability**

- Prometheus-style `/metrics` (prefixes `airway_im_gateway_*` /
  `airway_im_delivery_*`) plus an auto-refreshing `/dashboard` HTML page
  on both the gateway and the delivery worker.
- The backend admin status endpoint aggregates all three services.

**Airway integration**

- Go DSL migrations under `db/migrate` register on init when the plugin is
  enabled and run through the host binary's own `db:migrate`; SQLite, MySQL,
  and PostgreSQL are supported via the framework's schema compiler.
- REPL models (`User`) are exposed to the host REPL through the plugin
  contract; the file-storage API is available as in any Airway app.

## Repository layout

| Path | Role | Default port |
| --- | --- | --- |
| repo root (Go module `github.com/daqing/airway-im-plugin`) | The IM plugin (package `implugin`): IM API, admin API, internal API, migrations, REPL models | — |
| [`install/deps/im/gateway/`](install/deps/im/gateway/) | Standalone Go module (shipped to hosts via `plugin:install`): WebSocket gateway | 1910 |
| [`install/deps/im/delivery/`](install/deps/im/delivery/) | Standalone Go module (shipped to hosts via `plugin:install`): transactional-outbox publisher | 1920 |
| [`install/ignore/client/`](install/ignore/client/) | TypeScript demo client: multi-user group chat TUI + scripted end-to-end completeness proof | — |
| [`install/ignore/sdk/ts/`](install/ignore/sdk/ts/) | JavaScript/TypeScript SDK (npm package `airway-im-sdk-ts`): typed REST client, realtime gateway, and sequence-based sync engine, with built-in WeChat Mini Program and browser adapters | — |
| [`install/deps/im/docs/`](install/deps/im/docs/) | Design docs, API guides, OpenAPI contract, landing page (`index.html`), 中文文档 | — |

Key plugin packages:

| Package | Contents |
| --- | --- |
| `app/api/im_api` | Conversations and messages endpoints |
| `app/api/internal_api` | Gateway auth, credential minting, outbox poll/ack (secret-protected) |
| `app/api/me_api` | Profile lookup |
| `app/api/admin_api` | Admin console endpoints: sessions, users, status, moderation |
| `app/auth` | Credential mint/verify helpers and user auto-registration |
| `app/models` | `User` model + REPL registry |
| `app/repo` | Thin sqlx facade over the framework's `database/sql` pool |
| `app/utils` | ULID generator used for conversation/message/event IDs |
| `install/host/db/migrate` | Schema migrations (users, conversations, members, messages, idempotency, outbox, moderation) |

## Setup

Requirements: Go 1.26+, and SQLite locally (file or `:memory:`) or a
MySQL/PostgreSQL server. Optional: Docker for containerized
gateway/delivery (each ships a `Containerfile`).

### Using as a plugin

Enable the plugin in any Airway host application — either with the host's
installer or by hand:

```bash
go run . plugin:install github.com/daqing/airway-im-plugin   # in the host app
```

A local checkout can be installed with a `replace` directive pointing at this
directory instead. Enabling adds a blank import
`_ "github.com/daqing/airway-im-plugin"` to the host's `plugins.go`; on
import the plugin registers its routes (`/api/v1/...`, `/admin/api`), its Go
DSL migrations, and the `User` REPL model. The internal API (`/internal/v1`)
is served separately: when the host boots, the plugin starts a dedicated
listener for it (`IM_INTERNAL_ADDR`, default `127.0.0.1:1906`).
`plugin:install` also copies the plugin's `install/deps/` tree into the host's own
`deps/` directory — that is how the `gateway/` and `delivery/` companion
services arrive at `deps/im/gateway` and `deps/im/delivery` (their
`go.mod.templ` files are installed as `go.mod`; existing files are
never overwritten). Run the host's `db:migrate` to create the IM tables, and
set `IM_AUTH_SECRET` (plus `IM_INTERNAL_SECRET` for the realtime path) in the
host's environment.

### Running standalone

Any Airway host app with the plugin enabled is a complete IM backend. To run
the whole stack on its own, scaffold a fresh host, install the plugin, and
start the three services:

```bash
go install github.com/daqing/airway@latest
airway new myim && cd myim
go run . plugin:install github.com/daqing/airway-im-plugin

# edit .env: PORT=1905, DSN, IM_AUTH_SECRET, IM_INTERNAL_SECRET, IM_ADMIN_PASSWORD…
# (this repo's .env.example lists every IM variable)
go run . db:create    # create the database (when the driver supports it)
go run . db:migrate   # apply the IM migrations
go run . server       # start the backend on :1905
```

The IM migrations are Go DSL changes under `db/migrate/`; they register on
init through the plugin package and therefore run through the host binary
(`go run . db:migrate`), not the standalone `airway` CLI.

Then start the companion services from the `deps/` tree that
`plugin:install` copied into the host (all three must share
`IM_INTERNAL_SECRET`):

```bash
(cd deps/im/gateway && BACKEND_URL=http://127.0.0.1:1906 go run .)   # gateway :1910
(cd deps/im/delivery && BACKEND_URL=http://127.0.0.1:1906 \
                        GATEWAY_URL=http://127.0.0.1:1910 go run .)  # delivery :1920
```

`BACKEND_URL` points at the backend's internal API listener
(`IM_INTERNAL_ADDR`, default `127.0.0.1:1906`), not the public port — the
companion services only call `/internal/v1/*`.

Or run the whole stack with Docker: `plugin:install` drops a
`docker-compose.yml` at the host root that builds the backend and pulls in
the plugin's `deps/im/docker-compose.yml` (gateway + delivery) via Compose's
`include`, so `docker compose up --build` starts everything. If the host
already has its own `docker-compose.yml`, the install skips it — add
`include: [deps/im/docker-compose.yml]` to your file instead, and make sure
your app service is named `backend` with a healthcheck.

The companion services ship their module files as `go.mod.templ` (Go module
zips drop nested `go.mod` files); `plugin:install` materializes them as
`go.mod` in the host. To hack on this repository itself, point the host's
`go.mod` at the local checkout with a `replace` directive, and run
`just deps-setup` once here to materialize `install/deps/*/go.mod` for direct
`go run`.

### Upgrading from an older plugin version

Earlier plugin versions served the internal API (`/internal/v1/*`) on the
public port; it now lives on its own listener (`IM_INTERNAL_ADDR`, default
`127.0.0.1:1906`) and the public port answers 404 for it. After bumping the
plugin dependency in the host's `go.mod`:

- **Point `BACKEND_URL` of gateway and delivery at the internal listener**
  (e.g. `http://127.0.0.1:1906`). The shipped defaults already do; only
  deployments that set `BACKEND_URL` explicitly (typically to
  `http://<host>:1905`) must change it, or the realtime path stops working.
- If gateway/delivery run on different hosts than the backend, bind
  `IM_INTERNAL_ADDR` to an internal interface instead of the loopback
  default and keep that port firewalled from the public network.

Clients, the admin API, and the WebSocket wire protocol are unaffected.

## Authenticating users

Users are identified by the host application's `(name, uuid)` pair, signed
into an HMAC credential. A user is auto-registered in the `users` table the
first time a valid credential is presented — there is no separate
provisioning step. The full format, host-side signing examples (Go,
Node.js, Python), and rotation rules are in
[`install/deps/im/docs/design/identity.md`](install/deps/im/docs/design/identity.md).

For local development, the quickest way to get a credential is the
server-to-server minting endpoint on the internal listener:

```bash
curl -sX POST http://127.0.0.1:1906/internal/v1/credentials \
  -H "X-IM-Internal-Secret: <IM_INTERNAL_SECRET>" \
  -H 'Content-Type: application/json' \
  -d '{"uuid":"user-1","name":"alice","nickname":"Alice"}'
# → {"code":0,"data":{"credential":"im1.…","expires_at":"…"},"message":null}
```

Mint a second credential with a different `uuid`/`name` (e.g. bob), then
use the returned credentials with the IM API. Credentials default to a
24-hour TTL; mint fresh ones as needed.

## Using the IM API

All endpoints answer with the standard envelope
`{"code":0,"data":…,"message":null}` and authenticate via
`Authorization: Bearer <credential>`. The complete client contract —
including error codes and the WebSocket event format — is in
[`install/deps/im/docs/api/openapi.md`](install/deps/im/docs/api/openapi.md).

```bash
# Create a group with user 2 (bob) as a member
curl -sX POST http://127.0.0.1:1905/api/v1/group \
  -H "Authorization: Bearer <alice credential>" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Backend Team","member_ids":[2]}'
# → {"code":0,"data":{"id":"01M2ET18SA97XMFAG359T5TABH","kind":"group",…}}

# Create a direct conversation (idempotent: same pair → same conversation)
curl -sX POST http://127.0.0.1:1905/api/v1/conversations \
  -H "Authorization: Bearer <alice credential>" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"direct","member_ids":[2]}'

# Send a message — safe to retry with the same Idempotency-Key
curl -sX POST http://127.0.0.1:1905/api/v1/messages \
  -H "Authorization: Bearer <alice credential>" \
  -H "Idempotency-Key: 018f7d62-60d1-7d24-bfe1-3b4410f09a0a" \
  -d '{"conversation_id":"01M2ET18…","content":"Hello **team**!","content_type":"text/markdown"}'
# → {"code":0,"data":{"sequence":1,"sender":{…},…}}

# List your group conversations
curl -s "http://127.0.0.1:1905/api/v1/conversations?type=group" \
  -H "Authorization: Bearer <alice credential>"

# Inspect a conversation (kind + active members with roles)
curl -s http://127.0.0.1:1905/api/v1/conversations/<conversation_id> \
  -H "Authorization: Bearer <alice credential>"

# Synchronize messages missed while offline / disconnected
curl -s "http://127.0.0.1:1905/api/v1/conversations/<conversation_id>/messages?after_sequence=42" \
  -H "Authorization: Bearer <alice credential>"
```

HTTP API surface:

| Method & path | Purpose |
| --- | --- |
| `GET /api/v1/me` | Current user profile |
| `POST /api/v1/group` | Create a group conversation |
| `GET /api/v1/conversations?type=group` | List my group conversations |
| `POST /api/v1/conversations` | Create direct/group conversation |
| `GET /api/v1/conversations/:uuid` | Conversation details and members |
| `POST /api/v1/conversations/:uuid/members` | Add members to a group (owner/admin) |
| `DELETE /api/v1/conversations/:uuid/members/:user_id` | Remove a member from a group (owner/admin) |
| `GET /api/v1/conversations/:uuid/messages?after_sequence=N` | Message history / sync |
| `POST /api/v1/conversations/:uuid/messages` | Send message to a conversation |
| `POST /api/v1/messages` | Send message by conversation id |
| `/admin/api/*` | Admin console (login, status, users, moderation) |
| `/internal/v1/*` | Service-to-service (gateway auth, credential minting, outbox, ack) — secret-protected |

## Connecting over WebSocket

Clients connect to `ws://127.0.0.1:1910/ws` and authenticate with a
credential as the **first** application message:

```json
{"cmd":"auth","opts":["<credential>"]}
```

- Success: `{"code":0,"data":"OK","message":null}`; the connection is then
  bound to the user and may receive unsolicited event frames such as
  `{"event":"message.created","message_id":…,"conversation_id":…,"sequence":…}`.
- Failure/timeout: an error envelope, then close code `1008`. Reconnect with
  backoff, then catch up via `after_sequence` — never rely on the socket for
  missed messages. If the credential has expired, fetch a fresh one from the
  host backend before reconnecting.
- `{"cmd":"ping"}` answers `{"code":0,"data":"PONG"}`; protocol-level
  ping/pong runs automatically every 30 seconds.

Endpoint guides: [`install/deps/im/docs/api/messages.md`](install/deps/im/docs/api/messages.md),
[`install/deps/im/docs/api/me.md`](install/deps/im/docs/api/me.md), [`install/deps/im/docs/api/admin.md`](install/deps/im/docs/api/admin.md).

Clients don't have to implement this contract by hand: the JS/TS SDK
(`airway-im-sdk-ts`) under [`install/ignore/sdk/ts/`](install/ignore/sdk/ts/) wraps the REST API, the gateway
protocol (first-frame auth, heartbeat, backoff reconnect, event dedupe), and
sequence-based catch-up sync into a single typed `createIM()` facade, with
built-in adapters for WeChat Mini Programs and browsers.

## Configuration reference

| Variable | Service | Default | Description |
| --- | --- | --- | --- |
| `DSN` (or `AIRWAY_DSN`) | backend | — | Database DSN (sqlite/mysql/postgres) |
| `PORT` | backend | `1905` | HTTP listen port |
| `URL_PREFIX` | backend | — | Optional sub-path prefix behind a proxy |
| `STORAGE_DRIVER` / `STORAGE_*` | backend | `local` | File storage (see `.env.example`) |
| `IM_AUTH_SECRET` | backend, host | — | HMAC credential signing secret; **required** for authentication |
| `IM_AUTH_SECRET_PREVIOUS` | backend | — | Previous signing secret accepted during rotation (optional) |
| `IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD` | backend | — | Admin console credentials |
| `IM_GATEWAY_URL` | backend | `http://127.0.0.1:1910` | Gateway base URL for admin online-status and revocation kicks |
| `IM_INTERNAL_SECRET` | all | — | Shared secret for `/internal/v1/*` and the gateway deliver endpoint; **required** for realtime delivery |
| `IM_INTERNAL_ADDR` | backend | `127.0.0.1:1906` | Listen address of the internal API (`/internal/v1/*`); keep it off the public network |
| `ADMIN_GATEWAY_METRICS_URL` / `ADMIN_DELIVERY_METRICS_URL` | backend | gateway/delivery on localhost | Metrics endpoints aggregated by admin status |
| `GATEWAY_ADDR` | gateway | `:1910` | Gateway listen address |
| `BACKEND_URL` | gateway, delivery | `http://127.0.0.1:1906` | Backend internal API base URL |
| `GATEWAY_ALLOWED_ORIGINS` | gateway | — | Comma-separated `Origin` allowlist for browser clients |
| `DELIVERY_ADDR` | delivery | `:1920` | Delivery worker listen address |
| `GATEWAY_URL` | delivery | `http://127.0.0.1:1910` | Gateway base URL for delivery push |
| `DELIVERY_POLL_INTERVAL_MS` | delivery | `500` | Outbox polling interval (min 100) |

## Development

```bash
go test ./...                # unit tests (im/admin/me/routes…)
just deps-setup              # one-time: install/deps/*/go.mod.templ -> go.mod
(cd install/deps/im/gateway && go vet . && go build .)
(cd install/deps/im/delivery && go vet . && go build .)
```

The test suite covers conversation creation (direct uniqueness,
member validation), message persistence (idempotency, moderation masking,
outbox emission), profile lookup, admin endpoints, and route registration.
The full stack was additionally verified end-to-end locally:
credential minting → group creation → message send → outbox poll → gateway
push → WebSocket receipt → outbox ack.

