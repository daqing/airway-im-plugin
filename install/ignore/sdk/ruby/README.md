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
[identity.md](../../deps/im/docs/design/identity.md)):

- The credential is always obtained and delivered by **your backend**; the
  client never asks the IM server for one.
- The minting endpoint listens on `127.0.0.1:1906` by default; across hosts it
  is reached over a private network and is never exposed publicly. **A pure
  front-end without a server backend cannot integrate securely.**

### 2. Call the IM API

Every method returns the envelope's `data` (a Hash or Array) and raises
`AirwayIM::Error` on failure:

```ruby
im = AirwayIM::Client.new(
  api_url: "https://im.example.com",
  credential: credential,
  get_credential: -> { mint_fresh_credential },  # optional: auto-renew + retry once on 401/10001
)

im.me                                     # current user profile
conversation = im.create_direct("user-2") # direct conversation with the user whose uuid is "user-2" (get-or-create)
im.create_group(member_uuids: ["user-2", "user-3"], title: "Backend Team")
im.list_groups                            # groups I belong to
im.conversation(conversation["id"])       # conversation kind + members with roles

# Send a message: the Idempotency-Key is generated automatically and reused
# across network-failure retries, so a message is never duplicated
im.send_message(conversation["id"], "你好", content_type: "text/plain")

# History / sequence sync (catch up from the last cursor after going offline,
# returned in ascending order)
im.messages(conversation["id"], after_sequence: 42, limit: 100)
im.each_message(conversation["id"]).each { |msg| … }   # auto-paging over the full history

im.upload_file("/path/to/avatar.png", dir: "avatars")   # returns {key, url, size}
im.storage_url("avatars/202609/xxx.png")                # download URL
```

A service account (bot / system notifications) follows the same path: give
the bot a fixed `(uuid, name)`, mint a long-lived credential for it, and use
it to create conversations and send messages.

## API reference

### `AirwayIM::Client` (public API, default `:1905`)

| Method | Endpoint |
| --- | --- |
| `me` | `GET /api/v1/me` |
| `list_groups` | `GET /api/v1/conversations?type=group` |
| `create_conversation(kind:, member_uuids:, title: nil)` | `POST /api/v1/conversations` |
| `create_direct(other_uuid)` | Same (direct get-or-create) |
| `create_group(member_uuids:, title: nil)` | `POST /api/v1/group` |
| `conversation(uuid)` | `GET /api/v1/conversations/:uuid` |
| `add_members(uuid, member_uuids)` | `POST .../members` (owner/admin, idempotent) |
| `remove_member(uuid, user_uuid)` | `DELETE .../members/:user_uuid` (owner/admin) |
| `messages(uuid, after_sequence: nil, limit: nil)` | `GET .../messages` (raw paging, limit 1–200) |
| `each_message(uuid, after_sequence: 0, page_size: 100)` | Auto-paging Enumerator over the history, ascending |
| `send_message(conversation_id, content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `send_message_to(uuid, content, …)` | `POST /api/v1/conversations/:uuid/messages` |
| `upload_file(path or IO, filename:, dir:)` | `POST /api/v1/storage` (multipart) |
| `storage_url(key)` | File download URL |

Constructor options: `api_url` (required), `credential` (required),
`get_credential` (optional callback — on `10001`/401 automatically fetches a
fresh credential and retries once; the `credential` field is updated),
`timeout` (seconds, default 15). `content_type` accepts `text/markdown`
(default) and `text/plain`; content is limited to 32768 bytes and the server
normalizes CRLF to LF.

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
  im.send_message(id, "hi")
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
3. Treat `send_message`'s return value as authoritative — retries are handled
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

- API contract: [`deps/im/docs/api/openapi.md`](../../deps/im/docs/api/openapi.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../deps/im/docs/design/identity.md)
- Admin console: [`deps/im/docs/api/admin.md`](../../deps/im/docs/api/admin.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
