# airway-im-sdk-swift

The Swift SDK for [Airway IM](https://github.com/daqing/airway-im-plugin):
it wraps the backend's REST API, the WebSocket gateway protocol, and the
server-to-server credential-minting endpoint into ready-to-use Swift 6
interfaces, so iOS / macOS apps and server-side Swift never have to
implement credentials, the gateway first-frame authentication, heartbeats,
reconnect-with-backoff, or sequence-based catch-up sync themselves.

Built on Swift 6 concurrency (actors, `async`/`await`, `@Sendable`) with
**zero third-party dependencies** — only the Foundation and CryptoKit
system frameworks. Versioned in lockstep with the other Airway IM SDKs
(currently 0.7.0; the public APIs mirror `airway-im-sdk-ts` one to one).

## Features

- **Full REST coverage** — profile, conversations (direct get-or-create /
  groups / member management), messages (history, send, idempotent retry),
  and file upload, all strongly typed with automatic unwrapping of the
  `{code, data, message}` envelope.
- **Conversation handles** — `createDirect` / `getDirect` / `createGroup` /
  `openConversation` return per-conversation objects
  (`DirectConversation` / `GroupConversation`) with scoped `onMessage`
  events and `send` / `history`; the kind lives on the object, and the
  first message listener starts tracking automatically.
- **Realtime gateway** — automatically performs the `{"cmd":"auth"}`
  first-frame authentication, application-level heartbeat (25 s by
  default), exponential backoff reconnection (0.5 s → 10 s), and
  deduplication by `event_id`.
- **Sync engine** — maintains a per-conversation `sequence` cursor: on a
  `message.created` event it fills gaps via `after_sequence`, so messages
  that arrive out of order, duplicated, or while offline are eventually
  delivered through the `message` event **in order, without duplicates or
  loss**. Cursors can be persisted to `UserDefaults` so a cold start only
  fetches the delta.
- **Credential renewal** — when a credential expires (HTTP 401/10001 or a
  gateway auth failure), the SDK automatically calls `getCredential` for a
  fresh one and recovers; business code is unaffected.
- **Server-side credential minting** — a Swift backend (Vapor, …) obtains
  credentials through `InternalClient` (server-to-server, private network;
  never link it into client code) instead of raw HTTP calls.
- **Admin operations** — `AdminClient` covers the admin console API
  (status, users, revocation, message review, moderation) with transparent
  session management.

## Installation

Swift Package Manager. The package lives in this repository under
`install/ignore/sdk/swift/`; releases are tagged in lockstep with the
other Airway IM SDKs (currently 0.7.0).

- **Xcode**: *File → Add Package Dependencies… → Add Local…* and pick the
  `swift/` directory, then add the `AirwayIM` product to your app target.
- **Package.swift** (once the package is referenced from your manifest,
  by path during development or by its release repository afterwards):

```swift
dependencies: [
    .package(name: "AirwayIM", path: "../airway-im-plugin/install/ignore/sdk/swift")
]
```

Requires a Swift 6 toolchain; iOS 15+ / macOS 12+ / tvOS 15+ / watchOS 8+.

## Quick start

### 1. Obtain and deliver a credential from your own backend

The SDK contains no login logic. The user first logs in on your platform
(password, SMS code, …); once login succeeds, **your platform's own server
backend** reads `(uuid, name)` from its own user table and obtains the
credential **server-to-server** by calling the internal minting endpoint
(protected by `IM_INTERNAL_SECRET`), returning the finished credential
together with your own login response. A Swift backend does it with
`InternalClient` (server side only, never link it into client code):

```swift
import AirwayIM

let internal = InternalClient(
    internalURL: "http://127.0.0.1:1906",   // internal listener, private network only
    internalSecret: ProcessInfo.processInfo.environment["IM_INTERNAL_SECRET"]!)

let minted = try await internal.mintCredential(
    uuid: user.uuid,             // from your own user table
    name: user.username,
    nickname: user.displayName,  // optional; written only when present
    ttlSeconds: 86_400)          // optional; default 24 h, 0 = never expires

minted.credential  // "im1.…" — hand to the client with your login response
minted.expiresAt   // RFC 3339 UTC, when to re-mint; nil when ttlSeconds: 0
```

Backends in other languages call the same endpoint over plain HTTP. The
signing secret `IM_AUTH_SECRET` lives only on the IM server side of the
Airway project; your backend only needs `IM_INTERNAL_SECRET`. The client
holds no secret, only the finished credential (REST:
`Authorization: Bearer`; WebSocket: the first `auth` command).

**Prerequisite: your platform must have its own server backend.** A pure
client app without a server cannot integrate securely — the client has no
secure way to obtain a credential. The full trust model is documented in
[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md).

### 2. Exchange messages in an iOS / macOS app

```swift
import AirwayIM

let im = AirwayIM(
    apiURL: "https://im.example.com",   // IM backend (:1905), https in production
    wsURL: "wss://im.example.com",      // WebSocket gateway (:1910); the SDK connects to <wsURL>/ws
    credential: storedCredential,
    getCredential: {                    // called automatically when invalid
        await fetchNewCredentialFromYourBackend()
    })

// Connection control
im.onStatus { status in print("connection:", status) }
await im.connect()

// Direct chat: the kind lives on the object
let direct = try await im.createDirect(otherUserUUID)   // get-or-create
direct.onMessage { message in
    // ordered, deduplicated, gap-filled (including messages missed while
    // offline); the first listener starts tracking automatically
    print(message.sender.nickname ?? message.sender.username, message.content)
}
let backlog = try await direct.history()   // backlog so far, ascending sequence

// Sending (the SDK generates the Idempotency-Key automatically and retries
// network failures with the same key, so a message is never duplicated)
try await direct.send("你好", contentType: .plain)

// Group chat: the same model
let group = try await im.createGroup(title: "Team", memberUUIDs: [otherUserUUID])
group.onMessage { message in print(message.content) }
try await group.send("hello")
try await group.addMembers([anotherUserUUID])
```

### 3. App lifecycle recommendations

iOS keeps app sockets alive longer since iOS 13, but long backgrounding
still tears them down. Call `connect()` again when the app returns to the
foreground (it is idempotent); the SDK reconnects and catches up on sync
automatically:

```swift
NotificationCenter.default.addObserver(
    forName: UIApplication.willEnterForegroundNotification,
    object: nil, queue: .main) { _ in
        Task { await im.connect() }
    }
```

## API reference

### `AirwayIM` options

| Parameter | Required | Default | Description |
| --- | --- | --- | --- |
| `apiURL` | ✓ | — | IM backend URL (:1905); https in production |
| `wsURL` | | — | Gateway base URL (`:1910`) — pass it **without** a path; the SDK appends `/ws` itself. Without it only REST is used, no realtime |
| `credential` | ✓ | — | User credential issued by your backend |
| `getCredential` | | — | Fetches a fresh credential when the current one is rejected (HTTP 401/10001 or gateway auth failure); one renewal + one retry per request |
| `timeout` | | `15` | REST request timeout in seconds |
| `pingInterval` | | `25` | Application-level heartbeat interval in seconds, `0` disables |
| `persistSequences` | | `true` | Persist per-conversation sequence cursors |
| `sequenceStore` | | `UserDefaults` | Where cursors persist (`InMemorySequenceStore()` for memory-only) |
| `httpTransport` / `socketFactory` | | URLSession | Custom transport seams for tests and proxies |
| `autoConnect` | | `false` | Connect to the gateway immediately after creation |

### REST methods (`im.*`)

| Method | Endpoint |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createDirect(otherUserUUID)` | Direct get-or-create; returns the `DirectConversation` handle |
| `im.getDirect(otherUserUUID)` | The existing direct handle (nil when none) |
| `im.createGroup(title:memberUUIDs:)` | `POST /api/v1/group`; returns the `GroupConversation` handle |
| `im.openConversation(id)` | Open any conversation by id as a handle (kind from the registry, else one REST fetch) |
| `im.addMembers(conversationId, memberUUIDs:)` | `POST .../members` |
| `im.removeMembers(conversationId, userUUIDs:)` | `DELETE .../members/:user_uuid` |
| `im.history(conversationId, fromSequence:limit:)` | Pull history + start tracking sync |
| `im.listMessages(conversationId, afterSequence:limit:)` | `GET .../messages` (raw paging) |
| `im.sendGroupMessage(conversationId, content, …)` | `POST /api/v1/messages` |
| `im.sendDirectMessage(otherUserUUID, content, …)` | Get-or-create the direct conversation, then `POST /api/v1/messages` |
| `im.uploadFile(file, dir:)` | `POST /api/v1/storage` (multipart; `FileInput` = `.fileURL(URL)` or `.data(Data, filename:)`) |
| `im.storageURL(key)` | File download URL |

Send options: `contentType` (`.markdown` default / `.plain`),
`idempotencyKey` (auto-generated by default), `retries` (network-failure
retries, default 1).

### Conversation handles (`DirectConversation` / `GroupConversation`)

Handles are per-conversation objects — the kind lives on the object, and
the same conversation always yields the same handle. Anything emitted on a
handle also appears on the facade's global stream (below), and vice versa.

| Member | Description |
| --- | --- |
| `id` / `kind` | Conversation id; `.direct` or `.group` |
| `onMessage(…)` / `onMessageUpdated(…)` | Ordered message events; groups also fire `onMembersAdded` / `onMembersRemoved`. The first `onMessage` listener starts tracking automatically (history since the last persisted cursor, then realtime). Every registration returns a `Subscription` with `cancel()` |
| `history(fromSequence:limit:)` | Await the backlog; emits each message via `message` |
| `send(content, …)` | Send into this conversation (same idempotency/retry semantics as `sendGroupMessage`) |
| `listMessages(afterSequence:limit:)` | Raw ordered page, no state changes |
| `lastSequence()` / `forget()` | Sync cursor; drop all state for this conversation |
| `details()` | Conversation kind + members with roles, fresh from the API |
| group only: `title` | Title from creation time (nil when opened by id) |
| group only: `addMembers(_:)` / `removeMembers(_:)` | Member management; returns members with roles |

### Global stream (`im.on…`)

Conversation handles are the per-window API; the facade also exposes a
global stream, handy for unread badges or a unified inbox:

| Registration | Payload | Description |
| --- | --- | --- |
| `onMessage` | `(ChatMessage)` | New message, ordered, deduplicated, gap-filled |
| `onMessageUpdated` | `(ChatMessage)` | Message masked by moderation; content is already `***`; re-render by replacing |
| `onMembersAdded` | `MembersAddedInfo` | Group members added (including the added users themselves) |
| `onMembersRemoved` | `MembersRemovedInfo` | Group member removed (a kicked user is notified too) |
| `onStatus` | `ConnectionStatus` | `connecting / authenticating / online / reconnecting / offline / closed` |
| `onError` | `IMError` | Gateway auth failure, credential renewal failure, etc. |
| `onEvent` | Raw `GatewayEvent` | Every gateway frame; messages of conversations not yet tracked also arrive here |

Connection control: `im.connect()` (idempotent) / `im.disconnect()` /
`await im.isOnline` / `im.connectionStatus` / `await im.setCredential(_)` /
`await im.lastSequence(conversationId)` /
`await im.forgetConversation(conversationId)` (e.g. drop the sync cursor
after being kicked from a group).

### Message model (`ChatMessage`)

`send`, `sendDirectMessage`, `listMessages`, `history`, and realtime
`message` events all carry the same `ChatMessage`:

| Field | Type | Description |
| --- | --- | --- |
| `id` | string, 26-char ULID | Server-generated, globally unique message identifier; the stable key for deduplication. |
| `conversationId` | string, 26-char ULID | Conversation the message belongs to; route messages to their chat window by it. |
| `sender.uuid` | string | Author's stable identity uuid (identity is uuid-only across the API and events). |
| `sender.username` | string | Host-assigned account handle, stable for the account's lifetime. |
| `sender.nickname` | string? | Preferred display name; fall back to `username` when nil. |
| `sender.avatarUrl` | string? | Avatar image URL; render a placeholder when nil. |
| `content` | string | Message body, 1–32768 UTF-8 encoded bytes, CRLF normalized to LF by the server. A moderated message reads back as the literal `***`. |
| `contentType` | string | `text/markdown` (default) or `text/plain` — how to render `content`. |
| `createdAt` | string, RFC 3339 UTC | Server commit timestamp; convert to the viewer's local time zone for display. |
| `sequence` | integer ≥ 1 | Position within the conversation, allocated at commit. `(conversationId, sequence)` is the total order to sort by. |

Sort by `sequence`, never by `createdAt`. A retried send with the same
idempotency key returns the original message (same `id` and `sequence`);
the SDK already deduplicates and gap-fills realtime events for you.
`sender` reflects the author's current profile, not a send-time snapshot.

### `InternalClient` (internal minting endpoint, default `127.0.0.1:1906`)

| Method | Description |
| --- | --- |
| `mintCredential(uuid:name:nickname:avatarUrl:ttlSeconds:)` | Server-to-server credential minting; `ttlSeconds` defaults to 86400, capped at 2592000, `0` omits expiry; returns `MintedCredential` (`credential`, `expiresAt`) |

This type is for your backend only — a client app must never call it:
whoever can mint credentials can impersonate any user. Error code `10005`
here means the `X-IM-Internal-Secret` header is missing or wrong (not the
public API's permission denied); `10006` means the deployment has no
`IM_AUTH_SECRET` configured and cannot sign.

### `AdminClient` (admin console, `/admin/api`)

Logs in automatically on the first call (12-hour session); a mid-session
401 triggers one re-login and retry. Responses are `JSONValue` (the admin
shapes are open-ended; see
[`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)).

| Method | Description |
| --- | --- |
| `login` / `logout` | Explicit login / logout |
| `status` | Aggregated status: users, outbox, gateway/delivery metrics |
| `users` | Registered users (with `token_version`) |
| `revokeUser(uuid)` | Revoke all of a user's credentials and kick their connections |
| `groupConversations` | All group conversations (with member/message counts) |
| `conversationMessages(id)` | Message review (ascending) |
| `markIllegal(messageId)` | Mark illegal (idempotent): content masked to `***` for clients |

### `Credentials` (signing and verification helpers)

Local signing with `sign` is for trusted holders of `IM_AUTH_SECRET` on
the Airway project's own side; third-party platform backends never hold
that secret and must use `InternalClient` instead. (Apple platforms only —
uses the CryptoKit system framework.)

| Function | Description |
| --- | --- |
| `sign(secret:uuid:name:nickname:avatarUrl:tokenVersion:ttl:now:)` | Issues an `im1.<payload>.<sig>` HMAC-SHA256 credential; optional fields are written only when present, never clobbering an existing profile |
| `decode(credential)` | Returns the `Claims` (throws on malformed input) |
| `verifySignature(credential, secret:)` | Constant-time verification, returns `false` on malformed input |
| `isExpired(credential or claims, now:)` | Whether `exp` has passed; credentials without `exp` never expire |

### Error handling

All REST errors throw `IMError` (`error.code` — the envelope business
code, `error.status` — the HTTP status code, `status == 0` on transport
failure). Common checks:

```swift
do {
    try await im.sendGroupMessage(id, "hi")
} catch let error as IMError {
    if error.isAuthError { /* 10001: credential invalid/expired — wait for auto-renewal or re-login */ }
    else if error.code == ErrorCode.permissionDenied { /* 10005: not a member */ }
    else if error.code == ErrorCode.conversationNotFound { /* 11001 */ }
}
```

Full error codes: `10000` internal error, `10001` invalid credential,
`10003` invalid request, `10005` permission denied, `11001` conversation
not found, `11002` idempotency key reused with a different request.

## Message reliability model

The backend guarantees a monotonically increasing per-conversation
`sequence`; delivery is **at-least-once**. The SDK's sync engine takes
care of deduplication by `event_id` / `message_id`, ordering by
`sequence`, and gap-filling via `after_sequence`. Business code only needs
to:

1. open the conversation — a handle's first `onMessage` (or `history()`)
   starts tracking;
2. append-render in the `message` event (handle repeated messages
   idempotently by `message.id`);
3. render sends optimistically from `send()`'s return value and dedupe
   the realtime echo by `id`.

Conversations never tracked do not auto-fetch messages (avoids flushing
the whole history); handle them via `onEvent` for unread badges and
similar cases.

## Concurrency

`AirwayIM`, `RESTClient`, `AdminClient`, and the internal engine types are
actors: every method is `async` and safe to call from any task, and
instances are cheap to share across your whole app. Event handlers are
`@Sendable` closures invoked on arbitrary executors — hop to the main
actor yourself before touching UI. `Subscription.cancel()` unsubscribes;
keep the returned subscription alive for as long as the listener should
fire.

## Local development and verification

```bash
swift build
swift test        # 45 tests: signing vectors, envelope parsing, idempotent
                  # retry, credential renewal, sync engine, gateway
                  # reconnect/auth flows — against a stdlib-only stub server
```

The cross-language signing vector in `Tests` pins byte-for-byte
compatibility with the Node.js reference implementation
(`deps/im/docs/design/identity.md` §4.2), the same vector the Ruby and
PHP suites use. For end-to-end verification against a real stack (backend
:1905 / gateway :1910 / internal :1906, see the repository root README):
start the stack and follow "Quick start" step by step.

## Protocol reference

- API contract: [`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- Gateway protocol: [`deps/im/docs/design/gateway.md`](../../../deps/im/docs/design/gateway.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- Admin console: [`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
