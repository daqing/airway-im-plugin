# airway-im-sdk-ruby

The Ruby SDK for [Airway IM](https://github.com/daqing/airway-im-plugin): it
wraps the backend's REST API and the credential-minting endpoint into
ready-to-use Ruby interfaces, so Ruby projects (Rails / Sinatra / any Ruby
service) never have to implement minting calls, envelope parsing, idempotent
retries, or automatic credential renewal themselves.

Zero runtime dependencies — standard library only (`Net::HTTP` / `JSON` /
`OpenSSL` / `SecureRandom`).

**Positioning**: this is a **server-side SDK**. It covers every scenario in
which a Ruby backend integrates with IM — minting credentials for your users
through the internal API, calling the IM API as any user, and admin
operations. For realtime WebSocket send/receive in browsers or Mini Programs,
pair your front end with the JS/TS SDK ([`airway-im-sdk-ts`](../ts/), which
includes the sync engine); a Ruby side that needs to listen for new messages
(bots, notifications) can simply poll the sequence-based sync API (see
`each_message` below).

## Installation

```bash
gem install airway-im-sdk-ruby
```

Or in a Gemfile:

```ruby
gem "airway-im-sdk-ruby"
```

```ruby
require "airway-im-sdk-ruby"
```

## Quick start

### 1. Obtain a credential for your user

The SDK contains no login logic. The user first logs in on your platform
(password, SMS code, …); once login succeeds, **your server backend** reads
`(uuid, name)` from its own user table and obtains the IM credential by
calling the IM service's **internal minting endpoint** server-to-server,
returning it to the client together with your own login response. Your
backend only holds `IM_INTERNAL_SECRET`; the signing secret `IM_AUTH_SECRET`
never leaves the IM server, and minted credentials carry `token_version`, so
each user can be revoked through the admin API:

```ruby
internal = AirwayIM::InternalClient.new(
  internal_url: "http://127.0.0.1:1906",   # internal listener, private network only
  internal_secret: ENV["IM_INTERNAL_SECRET"],
)

minted = internal.mint_credential(uuid: "user-42", name: "alice",
                                  nickname: "Alice", ttl_seconds: 86_400)
credential = minted.credential          # "im1.…"
minted.expires_at                       # ISO8601 string; nil when ttl_seconds: 0
```

Third-party platform backends can obtain credentials **only** through the
internal minting endpoint: local signing (holding `IM_AUTH_SECRET` and
computing the HMAC yourself) is reserved for the Airway project's own
backend — platform-side code never holds the signing secret.

Two things must be kept straight (full trust model in
[identity.md](../../../deps/im/docs/design/identity.md)):

- The credential is always obtained and delivered by **your backend**; the
  client never asks the IM server for one.
- The minting endpoint listens on `127.0.0.1:1906` by default; across hosts it
  is reached over a private network and is never exposed publicly. **A pure
  front-end without a server backend cannot integrate securely.**

### 2. Call the IM API

Every method returns the envelope's `data` (a Hash or Array) and raises
`AirwayIM::Error` on failure. Conversation-getters return
`AirwayIM::DirectConversation` / `AirwayIM::GroupConversation` objects — one
object per conversation carrying its `id` and `kind` (plus `title` on
groups), with `send_message` / `list_messages` / `each_message` scoped to it.
Message responses come back as `AirwayIM::Message`, a Hash subclass —
`message["id"]`, `message.id`, and `message.sender.uuid` all work:

```ruby
im = AirwayIM::Client.new(
  api_url: "https://im.example.com",
  credential: credential,
  get_credential: -> { mint_fresh_credential },  # optional: auto-renew + retry once on 401/10001
)

direct = im.create_direct("user-2")       # DirectConversation object (get-or-create)
im.get_direct("user-2")      # the existing object with "user-2", or nil (read-only)
group = im.create_group(member_uuids: ["user-2", "user-3"], title: "Backend Team")
im.list_groups                            # groups I belong to
group.details                             # conversation kind + members with roles
im.open_conversation(group.id)            # any conversation by id, as a conversation object

# Send a message: the Idempotency-Key is generated automatically and reused
# across network-failure retries, so a message is never duplicated
direct.send_message("你好")
message = im.send_direct_message("user-2", "你好") # one call: get-or-create the direct conversation, then send
message.sequence                          # Message is a Hash with reader methods; message["sequence"] works too

group.add_members(["user-4"])             # group-only operations live on the object
group.remove_members("user-4")
group.details                             # members with roles, fresh from the API

# History / sequence sync (catch up from the last cursor after going offline,
# returned in ascending order)
im.list_messages(direct.id, after_sequence: 42, limit: 100)
im.each_message(direct.id).each { |msg| … }   # auto-paging over the full history

im.upload_file("/path/to/avatar.png", dir: "avatars")   # returns {key, url, size}
im.storage_url("avatars/202609/xxx.png")                # download URL
```

A service account (bot / system notifications) follows the same path: give
the bot a fixed `(uuid, name)`, mint a long-lived credential for it, and use
it to create conversations and send messages.

## API reference

### `AirwayIM::Client` (public API, default `:1905`)

Method names mirror the TS SDK one to one (snake_case here, camelCase there):
`create_direct` ↔ `createDirect`, `get_direct` ↔
`getDirectConversation`, `create_group` ↔
`createGroup`, `list_messages` ↔ `listMessages`, and so on. The same conversation objects
exist here — `DirectConversation` / `GroupConversation` with
`send_message` / `list_messages` / `each_message` / `details` (groups:
`add_members` / `remove_members`) and `open_conversation(id)` — minus the realtime
layer: the TS objects' `on("message")` subscriptions, gateway, and sync
engine have no Ruby counterpart in v1; poll `each_message` instead.

| Method | Endpoint |
| --- | --- |
| `me` | `GET /api/v1/me` |
| `list_groups` | `GET /api/v1/conversations?type=group` |
| `create_direct(other_uuid)` | Same (direct get-or-create) |
| `get_direct(other_uuid)` | `GET /api/v1/conversations/direct/:user_uuid` (nil when none) |
| `create_group(member_uuids:, title: nil)` | `POST /api/v1/group` |
| `open_conversation(conversation_id)` | `GET /api/v1/conversations/:uuid`, returned as a conversation object (kind/title resolved from the details) |
| `add_members(conversation_id, member_uuids)` | `POST .../members` (owner/admin, idempotent) |
| `remove_members(conversation_id, user_uuids)` | `DELETE .../members/:user_uuid` (owner/admin) |
| `list_messages(conversation_id, after_sequence: nil, limit: nil)` | `GET .../messages` (raw paging, limit 1–200) |
| `each_message(conversation_id, after_sequence: 0, page_size: 100)` | Auto-paging Enumerator over the history, ascending |
| `send_group_message(conversation_id, content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `send_direct_message(other_uuid, content, …)` | Get-or-create the direct conversation, then `POST /api/v1/messages` |
| `upload_file(path or IO, filename:, dir:)` | `POST /api/v1/storage` (multipart) |
| `storage_url(key)` | File download URL |

Constructor options: `api_url` (required), `credential` (required),
`get_credential` (optional callback — on `10001`/401 automatically fetches a
fresh credential and retries once; the `credential` field is updated),
`timeout` (seconds, default 15). `content_type` accepts `text/markdown`
(default) and `text/plain`; content is limited to 32768 bytes and the server
normalizes CRLF to LF.

### Conversation objects

`create_direct` and `get_direct` return an
`AirwayIM::DirectConversation`; `create_group` and
`open_conversation` resolve to an `AirwayIM::GroupConversation` (or
`DirectConversation`) using the details lookup. Every object carries `id`
and `kind` and scopes the conversation API to itself:

| Handle method | Delegates to |
| --- | --- |
| `send_message(content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `list_messages(after_sequence:, limit:)` | `list_messages(id, …)` |
| `each_message(after_sequence: 0, page_size: 100)` | `each_message(id, …)` |
| `add_members(member_uuids)` — groups | `add_members(id, member_uuids)` |
| `remove_members(user_uuids) — groups | `remove_members(id, user_uuids)` |
| `details` | `GET /api/v1/conversations/:uuid` |

`group.title` carries the creation-time title (`nil` when created without
one or when opened by id); `details` returns the live value.

### Message object

`send_message`, `send_direct_message`, `list_messages`, and `each_message` return
`AirwayIM::Message` objects — Hash subclasses with reader methods, so
`message["id"]`, `message.id`, and `message.sender.uuid` all work:

| Field | Type | Description |
| --- | --- | --- |
| `id` | string, 26-char ULID | Server-generated, globally unique message identifier; the stable key for deduplication. |
| `conversation_id` | string, 26-char ULID | Conversation the message belongs to; route messages to their chat window by it. |
| `sender.uuid` | string | Author's stable identity uuid (identity is uuid-only across the API and events). |
| `sender.username` | string | Host-assigned account handle, stable for the account's lifetime. |
| `sender.nickname` | string \| nil | Preferred display name; fall back to `username` when nil. |
| `sender.avatar_url` | string \| nil | Avatar image URL; render a placeholder when nil. |
| `content` | string | Message body, 1–32768 UTF-8 encoded bytes, CRLF normalized to LF by the server. A moderated message reads back as the literal `***`. |
| `content_type` | string | `text/markdown` (default) or `text/plain` — how to render `content`. |
| `created_at` | string, RFC 3339 UTC | Server commit timestamp; convert to the viewer's local time zone for display. |
| `sequence` | integer ≥ 1 | Position within the conversation, allocated at commit. `(conversation_id, sequence)` is the total order to sort by; pass the last seen value as `after_sequence` to page history. |

Sort by `sequence`, never by `created_at`. A retried send with the same
`Idempotency-Key` returns the original message (same `id` and `sequence`), so
retries never create a second local entry. `sender` reflects the author's
current profile, not a send-time snapshot.

### `AirwayIM::Credentials` (signing and verification helpers)

Local signing with `sign` is for trusted holders of `IM_AUTH_SECRET` on the
Airway project's own side; third-party platform backends never hold that
secret and must use `InternalClient#mint_credential` instead.

| Method | Description |
| --- | --- |
| `sign(secret:, uuid:, name:, nickname: nil, avatar_url: nil, token_version: nil, ttl: 86_400, now: Time.now)` | Issues an `im1.<payload>.<sig>` HMAC-SHA256 credential; optional fields are written only when present, never clobbering an existing profile |
| `decode(credential)` | Returns the claims (raises `Error` on malformed input) |
| `verify_signature?(credential, secret)` | Constant-time verification, returns `false` on malformed input |
| `expired?(credential or claims, now:)` | Whether `exp` has passed; credentials without `exp` never expire |

### `AirwayIM::InternalClient` (internal minting endpoint, default `127.0.0.1:1906`)

| Method | Description |
| --- | --- |
| `mint_credential(uuid:, name:, nickname: nil, avatar_url: nil, ttl_seconds: nil)` | Server-to-server credential minting; `ttl_seconds` defaults to 86400, capped at 2592000, `0` omits expiry; returns `MintedCredential(credential, expires_at)` |

### `AirwayIM::AdminClient` (admin console, `/admin/api`)

Logs in automatically on the first call (12-hour session); a mid-session 401
triggers one re-login and retry.

| Method | Description |
| --- | --- |
| `login` / `logout` | Explicit login / logout |
| `status` | Aggregated status: user count, online users, gateway/delivery metrics |
| `users` | Registered users (with `token_version`) |
| `revoke_user(uuid)` | Revoke all of a user's credentials and kick their connections |
| `group_conversations` | All group conversations (with member/message counts) |
| `conversation_messages(conversation_id)` | Group message review (ascending) |
| `mark_illegal(message_id)` | Mark illegal (idempotent): content masked to `***` for clients |

## Error handling

All errors raise `AirwayIM::Error` (`err.code` — the envelope business code,
`err.status` — the HTTP status code, `status == 0` and `code == -1` on
transport failure):

```ruby
begin
  im.send_group_message(id, "hi")
rescue AirwayIM::Error => e
  if e.auth_error?          # 10001 / 401: credential invalid or expired — renew or re-mint
  elsif e.code == AirwayIM::ErrorCode::PERMISSION_DENIED      # 10005
  elsif e.code == AirwayIM::ErrorCode::CONVERSATION_NOT_FOUND # 11001
  end
end
```

Full error codes: `10000` internal error, `10001` invalid credential,
`10003` invalid request, `10005` permission denied, `11001` conversation not
found, `11002` idempotency key reused with a different request.

## Message reliability model

The backend guarantees a monotonically increasing per-conversation
`sequence`; delivery is **at-least-once**. Recommended practice for
server-side integrations:

1. Catch up with `messages(uuid, after_sequence: last_cursor)` or
   `each_message`;
2. Process idempotently by `msg["id"]` and order by `sequence`;
3. Treat `send_group_message`'s return value as authoritative — retries are handled
   by the SDK (the same `Idempotency-Key` guarantees no duplicates).

Realtime reception and automatic gap-filling for end-user clients (Mini
Programs / browsers) belong to [`airway-im-sdk-ts`](../ts/); the Ruby polling
interval is simply your business latency requirement.

## Thread safety

`Client` / `InternalClient` / `AdminClient` instances are safe to share
across threads (Puma-style servers): every request uses an independent
connection, and credential refresh / login are one-shot overwrites.
Serialize `AdminClient#login` / `logout` against business calls yourself if
they run concurrently.

## Local development and verification

```bash
rake test          # minitest: signing vectors, envelope parsing, idempotent retry, credential renewal, multipart…
gem build airway-im-sdk-ruby.gemspec
```

The tests ship a stdlib-only TCPServer stub HTTP server and exercise real
`Net::HTTP` I/O. For end-to-end verification against a real stack (backend
:1905 / internal :1906, see the repository root README): start the stack and
follow "Quick start" step by step; credential-signing correctness is pinned
by a cross-language vector from the Node.js reference implementation
(`test/credentials_test.rb`).

## Protocol reference

- API contract: [`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- Admin console: [`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
