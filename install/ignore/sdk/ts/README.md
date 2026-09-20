# airway-im-sdk-ts

The JS/TS SDK for [Airway IM](https://github.com/daqing/airway-im-plugin),
covering WeChat Mini Programs and browsers (Vue / React / plain web pages). It
wraps the backend's REST API, the WebSocket gateway protocol, and the
server-to-server credential-minting endpoint into ready-to-use TypeScript
interfaces, so clients never have to implement credentials, the
gateway first-frame authentication, heartbeats, reconnect-with-backoff, or
sequence-based catch-up sync themselves.

Zero runtime dependencies; ships both CommonJS and ESM builds (`dist/cjs` /
`dist/esm`). Compatible with the WeChat DevTools "Build npm" flow, Taro /
uni-app, and browser frameworks such as Vue and React.

## Features

- **Full REST coverage** — profile, conversations (direct get-or-create /
  groups / member management), messages (history, send, idempotent retry),
  and file upload, all strongly typed with automatic unwrapping of the
  `{code,data,message}` envelope.
- **Conversation objects** — `createDirect` / `getDirect` /
  `createGroup` / `openConversation` return per-conversation
  objects (`DirectConversation` / `GroupConversation`) with scoped
  `on("message")` events and `send()` / `history()`; the kind lives on the
  object, and the first message listener starts tracking automatically.
- **Realtime gateway** — automatically performs the `{"cmd":"auth"}` first-frame
  authentication, application-level heartbeat (25 s by default), exponential
  backoff reconnection (0.5 s → 10 s), and deduplication by `event_id`.
- **Sync engine** — maintains a per-conversation `sequence` cursor: on a
  `message.created` event it fills gaps via `after_sequence`, so messages that
  arrive out of order, duplicated, or while offline are eventually delivered
  through the `message` event **in order, without duplicates or loss**. Cursors
  can be persisted to local storage so a cold start only fetches the delta.
- **Credential renewal** — when a credential expires (HTTP 401/10001 or a
  gateway auth failure), the SDK automatically calls `getCredential` for a
  fresh one and recovers; business code is unaffected.
- **Server-side credential minting** — a Node backend obtains credentials
  through the SDK's `InternalClient` (server-to-server, private network;
  never bundle it into client code) instead of raw HTTP calls.
- **Mini Program friendly** — the built-in `wechatAdapter` is built on
  `wx.request` / `wx.connectSocket` / `wx.uploadFile` / `wx.*StorageSync`, and
  automatically restores the connection on `wx.onAppShow` (Mini Programs kill
  sockets when backgrounded).
- **Browser support** — ships a built-in `browserAdapter`
  (fetch / WebSocket / FormData / localStorage); works in Vue / React / plain
  pages out of the box, and reconnects when a tab becomes visible again.

## Installation

```bash
npm install airway-im-sdk-ts
```

WeChat DevTools: run "Tools → Build npm", then `import` as usual.
Taro / uni-app can import directly (the ESM build is preferred).

## Quick start

### 1. Obtain and deliver a credential from your own backend

The SDK contains no login logic and does not handle authentication. The user
first logs in on your platform (password, SMS code, WeChat `code2session`,
…). Once login succeeds, **your platform's own server backend** (PHP, Java, or
any language) reads `(name, uuid)` from its own user table and obtains the
credential **server-to-server** by calling the internal minting endpoint
`POST /internal/v1/credentials` on the Airway project's IM service (with
`X-IM-Internal-Secret`), then returns the finished credential together with
your own session token in the login response (valid for 24 hours; mint a
fresh one before expiry).

That call, spelled out in a Node backend — one call with the SDK's
`InternalClient` (server side only, never bundle it into client code):

```ts
import { InternalClient } from "airway-im-sdk-ts";

const internal = new InternalClient({
  internalUrl: "http://127.0.0.1:1906",            // internal listener, private network only
  internalSecret: process.env.IM_INTERNAL_SECRET!, // the deployment's IM_INTERNAL_SECRET
});

const minted = await internal.mintCredential({
  uuid: user.uuid,            // from your own user table
  name: user.username,
  nickname: user.displayName, // optional; written only when present
  ttlSeconds: 86_400,         // optional; default 24 h, 0 = never expires
});

minted.credential;  // "im1.…" — hand to the client with your login response
minted.expiresAt;   // RFC 3339 UTC, when to re-mint; null when ttlSeconds: 0
```

Backends in other languages call the same endpoint over plain HTTP; the
full contract is documented under *Credential minting API* in the API
reference below.

Two things must be kept straight:

- The credential is always obtained and delivered by **your backend**; the
  client never asks the IM server for one. The IM public API (the Airway
  project's `:1905`) only verifies credentials and never issues them to
  clients. Minting happens exclusively on the internal endpoint
  `POST /internal/v1/credentials`, protected by `IM_INTERNAL_SECRET` and
  intended only for server-to-server calls. That endpoint listens on
  `127.0.0.1:1906` by default: when your backend is deployed on the same
  machine as the Airway project you can call it directly; on different hosts
  both must share a private network (same VPC / data-center intranet, or a
  VPN / private line) with `IM_INTERNAL_ADDR` bound to a private interface by
  the Airway operator — it is never exposed to the public internet.
- The signing secret `IM_AUTH_SECRET` lives only on the IM server side of the
  Airway project; your backend only needs `IM_INTERNAL_SECRET`. `(name, uuid)`
  is not needed — and should never be sent — by the client: it already lives
  in your database. The client holds no secret, only the finished credential
  (REST: `Authorization: Bearer`; WebSocket: the first `auth` command).

**Prerequisite: your platform must have its own server backend.** A pure
front-end client without a server (a serverless Mini Program, a static page)
cannot integrate securely — the client has no secure way to obtain a
credential and nowhere safe to keep even `IM_INTERNAL_SECRET`; whoever could
mint credentials directly could impersonate any user.

For other languages and the full rules see
[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md).

### 2. Initialize and exchange messages in a Mini Program

```ts
import { createClient, wechatAdapter } from "airway-im-sdk-ts";

const im = createClient({
  apiUrl: "https://im.example.com",   // IM backend (:1905), must be https
  wsUrl: "wss://im.example.com",      // WebSocket gateway (:1910); the SDK connects to <wsUrl>/ws
  credential: wx.getStorageSync("im-credential"),
  adapter: wechatAdapter(),           // required: browsers/Node use browserAdapter()
  getCredential: () =>                // called automatically when invalid
    fetchNewCredentialFromYourBackend(),
});

im.on("status", (s) => console.log("connection:", s));
im.connect();

// Direct chat: the kind lives on the object
const direct = await im.createDirect(otherUserUUID);   // get-or-create
direct.on("message", (msg, source) => {
  // ordered, deduplicated, gap-filled (including messages missed while
  // offline); the first listener starts tracking automatically
  console.log(`[${source}]`, msg.sender.nickname, msg.content);
});
const history = await direct.history();   // backlog so far, ascending sequence

// Sending (the SDK generates the Idempotency-Key automatically and retries
// network failures with the same key, so a message is never duplicated)
await direct.send("你好", { contentType: "text/plain" });

// Group chat: the same model
const group = await im.createGroup("Team", [otherUserUUID]);
group.on("message", (msg) => console.log(msg.content));
await group.send("hello");
```

### 3. Page lifecycle recommendations

When a Mini Program is backgrounded the OS tears down the socket. By default
the SDK (via `wx.onAppShow`) reconnects and catches up on sync automatically
when the app returns to the foreground; if you manage the lifecycle yourself,
in your `App`:

```ts
App({
  onShow() { im.connect(); },     // connect() is idempotent
  onHide() { /* you may keep the connection; the SDK recovers on its own */ },
});
```

## API reference

### `createClient(options)`

| Option | Required | Default | Description |
| --- | --- | --- | --- |
| `apiUrl` | ✓ | — | IM backend URL (:1905); https in production |
| `wsUrl` | | — | Gateway base URL (`:1910`) — pass it **without** a path; the SDK appends `/ws` itself (`wsUrl: "ws://localhost:1910"` → default gateway endpoint `ws://localhost:1910/ws`; `wss://…` behind a TLS-terminating proxy). Without it only REST is used, no realtime |
| `credential` | ✓ | — | User credential issued by your backend |
| `getCredential` | | — | `() => Promise<string>`, fetches a fresh credential when invalid |
| `adapter` | ✓ | — | Platform adapter: `wechatAdapter()` in Mini Programs, `browserAdapter()` in browsers/Node; required, never defaulted — `createClient` throws if it is missing |
| `timeoutMs` | | `15000` | REST request timeout |
| `pingIntervalMs` | | `25000` | Application-level heartbeat interval, `0` disables |
| `persistSequences` | | `true` | Persist per-conversation sequence cursors |
| `autoConnect` | | `false` | Connect to the gateway immediately after creation |

### REST methods (`im.*`)

| Method | Endpoint |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createDirect(otherUserUUID)` | Direct get-or-create; returns the `DirectConversation` object |
| `im.getDirect(otherUserUUID)` | The existing direct conversation object (null when none) |
| `im.createGroup(title, memberUUIDs)` | `POST /api/v1/group`; returns the `GroupConversation` object |
| `im.openConversation(id)` | Open any conversation by id as a conversation object (kind from the registry, else one REST fetch) |
| `im.addMembers(conversationId, memberUUIDs)` | `POST .../members` |
| `im.removeMembers(conversationId, userUUIDs: string[])` | `DELETE .../members/:user_uuid` |
| `im.history(conversationId, {fromSequence?, limit?})` | Pull history + start tracking sync |
| `im.listMessages(conversationId, {afterSequence?, limit?})` | `GET .../messages` (raw paging) |
| `im.sendGroupMessage(conversationId, content, opts?)` | `POST /api/v1/messages` |
| `im.sendDirectMessage(otherUserUUID, content, opts?)` | Get-or-create the direct conversation, then `POST /api/v1/messages` |
| `im.uploadFile(filePath \| File, dir?)` | `POST /api/v1/storage` |
| `im.storageUrl(key)` | File download URL (for `<image>` / `wx.downloadFile`) |

`sendGroupMessage` opts: `contentType` (`text/markdown` default / `text/plain`),
`idempotencyKey` (auto-generated by default), `retries` (network-failure
retries, default 1).

### Conversation objects (`DirectConversation` / `GroupConversation`)

Handles are per-conversation objects — the kind lives on the object, and the
same conversation always yields the same object. Anything emitted on a conversation object
also appears on the facade's global stream (below), and vice versa.

| Member | Description |
| --- | --- |
| `id` / `kind` | Conversation id; `"direct"` or `"group"` |
| `on(event, listener)` | `message` `(msg, "history"\|"realtime")`, `message.updated` `(msg)`; groups also fire `members.added` / `members.removed`. The first `message` listener starts tracking automatically (history since the last persisted cursor, then realtime) |
| `history({fromSequence?, limit?})` | Await the backlog; emits with source `"history"` |
| `send(content, opts?)` | Send into this conversation (same idempotency/retry semantics as `sendGroupMessage`) |
| `lastSequence()` / `forget()` | Sync cursor; drop all state for this conversation |
| `details()` | Conversation kind + members with roles, fresh from the API |
| group only: `title` | Title from creation time |
| group only: `addMembers(uuids)` / `removeMembers(uuid)` | Member management; members with roles |

```ts
const group = await im.createGroup("Team", [aliceUuid, bobUuid]);
group.on("message", (msg) => renderGroupMessage(msg));
await group.send("hello");
```

### Message object (`ChatMessage`)

`sendGroupMessage`, `sendDirectMessage`, `listMessages`, `history`, and realtime
`message` events all carry the same `ChatMessage` shape:

| Field | Type | Description |
| --- | --- | --- |
| `id` | string, 26-char ULID | Server-generated, globally unique message identifier; the stable key for deduplication. |
| `conversation_id` | string, 26-char ULID | Conversation the message belongs to; route messages to their chat window by it. |
| `sender.uuid` | string | Author's stable identity uuid (identity is uuid-only across the API and events). |
| `sender.username` | string | Host-assigned account handle, stable for the account's lifetime. |
| `sender.nickname` | string \| null | Preferred display name; fall back to `username` when null. |
| `sender.avatar_url` | string \| null | Avatar image URL; render a placeholder when null. |
| `content` | string | Message body, 1–32768 UTF-8 encoded bytes, CRLF normalized to LF by the server. A moderated message reads back as the literal `***`. |
| `content_type` | string | `text/markdown` (default) or `text/plain` — how to render `content`. |
| `created_at` | string, RFC 3339 UTC | Server commit timestamp; convert to the viewer's local time zone for display. |
| `sequence` | number ≥ 1 | Position within the conversation, allocated at commit. `(conversation_id, sequence)` is the total order to sort by; pass the last seen value as `afterSequence` to page history. |

Sort by `sequence`, never by `created_at`. A retried send with the same
`idempotencyKey` returns the original message (same `id` and `sequence`); the
SDK already deduplicates and gap-fills realtime events for you. `sender`
reflects the author's current profile, not a send-time snapshot.

### Global stream (`im.on(name, handler)`)

Conversation objects are the per-window API; the facade also exposes a global
stream, handy for unread badges or a unified inbox:

| Event | Payload | Description |
| --- | --- | --- |
| `message` | `(msg, "history"\|"realtime")` | New message, ordered, deduplicated, gap-filled |
| `message.updated` | `(msg)` | Message masked by moderation; content is already `***`; re-render by replacing |
| `members.added` | `{conversationId, addedUserUuids, event}` | Group members added (including the added users themselves) |
| `members.removed` | `{conversationId, removedUserUuid, event}` | Group member removed (a kicked user is notified too) |
| `status` | `ConnectionStatus` | `connecting / authenticating / online / reconnecting / offline / closed` |
| `error` | `Error` | Gateway auth failure, credential renewal failure, etc. |
| `event` | Raw `GatewayEvent` | Every gateway frame; messages of conversations not yet `history()`-tracked also arrive here |

Connection control: `im.connect()` (idempotent) / `im.disconnect()` /
`im.isOnline` / `im.connectionStatus` / `im.setCredential(cred)` /
`im.lastSequence(conversationId)` / `im.forgetConversation(conversationId)`
(e.g. drop the sync cursor after being kicked from a group).

The global `message` event carries only `conversation_id` — the wire format
has no conversation kind. Use `im.openConversation(msg.conversation_id)` when
you need the kind or a scoped subscription.

### Error handling

All REST errors throw `IMError` (`err.code` — the envelope business code,
`err.status` — the HTTP status code, `status === 0` on transport failure).
Common checks:

```ts
import { IMError, ErrorCode } from "airway-im-sdk-ts";

try {
  await im.sendGroupMessage(id, "hi");
} catch (err) {
  if (err instanceof IMError) {
    if (err.isAuthError) /* 10001: credential invalid/expired — wait for auto-renewal or re-login */;
    else if (err.code === ErrorCode.PermissionDenied) /* 10005: not a member */;
    else if (err.code === ErrorCode.ConversationNotFound) /* 11001 */;
  }
}
```

Full error codes: `10000` internal error, `10001` invalid credential,
`10003` invalid request, `10005` permission denied, `11001` conversation not
found, `11002` idempotency key reused with a different request.

### Credential minting API (server-to-server)

TS/Node backends use the SDK's `InternalClient` for this (see Quick start
step 1); the endpoint itself is plain HTTP, so backends in any language can
call it directly.

`POST /internal/v1/credentials` on the internal listener (default
`127.0.0.1:1906`, private network only), authenticated by the
`X-IM-Internal-Secret` header matching the deployment's `IM_INTERNAL_SECRET`.
This endpoint is for your backend only — a browser page or Mini Program must
never call it: whoever can mint credentials can impersonate any user.

On the first mint the user is registered with IM under the `uuid` you supply
(that same uuid is what your clients later pass to `createDirect` /
`sendDirectMessage`); the credential carries the user's current
`token_version`, so revoking the user through the admin API immediately
invalidates every credential minted before it.

Request body (JSON):

| Field | Required | Description |
| --- | --- | --- |
| `uuid` | ✓ | Stable user id from your own user table, 1–64 characters |
| `name` | ✓ | Account handle, 1–64 characters |
| `nickname` | | Display name; written only when present, so omitting it never clobbers a profile stored earlier |
| `avatar_url` | | Avatar URL, up to 2048 characters; same write-only-when-present rule |
| `ttl_seconds` | | Credential lifetime in seconds; default `86400` (24 h), max `2592000` (30 days), `0` = never expires |

Success returns `{code: 0, data: {credential, expires_at?}}`: `credential`
is the finished `im1.<payload>.<sig>` token the client presents as
`Authorization: Bearer` (REST) or the first gateway `auth` command;
`expires_at` (RFC 3339 UTC) is omitted when `ttl_seconds` is `0`.

Error codes: `10003` invalid JSON, missing/oversized `uuid`/`name`, or
`ttl_seconds` out of range; `10005` here means the `X-IM-Internal-Secret`
header is missing or wrong (not the public API's permission denied);
`10006` the Airway deployment has no `IM_AUTH_SECRET` configured and cannot
sign; `10000` internal error.

## Message reliability model

The backend guarantees a monotonically increasing per-conversation
`sequence`; delivery is **at-least-once**. The SDK's sync engine takes care
of deduplication by `event_id` / `message_id`, ordering by `sequence`, and
gap-filling via `after_sequence`. Business code only needs to:

1. open the conversation — a conversation object's first `on("message")` (or `history()`)
   starts tracking;
2. append-render in the `message` event (handle repeated messages idempotently
   by `msg.id`);
3. render sends optimistically from `send()`'s return value and dedupe
   the realtime echo by `id`.

Conversations never tracked do not auto-fetch messages (avoids flushing the
whole history); handle them via the global `event` event for unread badges
and similar cases.

## Mini Program domain configuration

In the WeChat MP console → Development → Development Settings → Server
Domains, configure:

- **request legal domains**: `https://im.example.com` (REST + upload)
- **socket legal domains**: `wss://im.example.com` (realtime gateway)

For local debugging, check "do not verify legal domains" in the DevTools and
use `http://127.0.0.1:1905` / `ws://127.0.0.1:1910` (the gateway endpoint the
SDK connects to is `ws://127.0.0.1:1910/ws`; the gateway itself serves plain
WS — `wss` appears only once a reverse proxy terminates TLS).

## Using in Vue / React / plain web pages

The SDK core is platform-agnostic and takes an explicit adapter — there is no
default: `wechatAdapter()` in Mini Programs, the built-in `browserAdapter()`
(fetch / WebSocket / FormData / localStorage) in browsers and Node:

```ts
import { createClient, browserAdapter } from "airway-im-sdk-ts";

export const im = createClient({
  apiUrl: "https://im.example.com",
  wsUrl: "wss://im.example.com",
  credential: localStorage.getItem("im-credential") ?? "",
  adapter: browserAdapter(),
  getCredential: fetchFreshCredential, // auto-renewal when invalid
});
```

Behavior is identical to the Mini Program side: `browserAdapter` reconnects
and catches up on sync when the tab becomes visible again
(`visibilitychange`), and `im.uploadFile(file, dir)` accepts the `File` /
`Blob` from `<input type="file">` directly (WeChat still takes a local path
string).

Two browser-only caveats (Mini Programs don't have them):

1. **REST cross-origin (CORS)** — the backend currently has no CORS
   middleware, so cross-origin browser requests are blocked. Prefer reverse-
   proxying the API to a same-origin path with Nginx, or add CORS middleware
   to the backend.
2. **Gateway Origin check** — browser WebSocket handshakes carry an `Origin`
   header; add your front-end domain to the gateway's
   `GATEWAY_ALLOWED_ORIGINS` (comma-separated) or the handshake is refused.

Credentials are still issued by your own backend and handed to the front end;
`IM_AUTH_SECRET` must never appear in browser code.

Other platforms (Alipay / Douyin Mini Programs, Node, …) can provide their own
adapter implementing the `IMAdapter` interface from `src/adapter.ts`;
`test/node-adapter.ts` is a reference Node implementation.

## Local development and verification

```bash
pnpm install
pnpm build          # outputs dist/esm + dist/cjs (with .d.ts)
pnpm typecheck
```

End-to-end verification against a real stack (backend :1905 / gateway :1910 /
delivery :1920, see the repository root README):

```bash
IM_INTERNAL_URL=http://127.0.0.1:1906 pnpm verify
```

The script covers: credential minting and registration, direct/group
send-receive, realtime fan-out and ordering, idempotent replay and 11002,
member add/remove and events, kicked-member permissions, reconnect catch-up
sync, moderation masking via `message.updated`, and file upload; then re-runs
the core flows with `browserAdapter` (including File/Blob upload and
localStorage persistence). `IM_INTERNAL_URL`/`IM_INTERNAL_SECRET` and other
values can be overridden through environment variables.

## Protocol reference

- API contract: [`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- Gateway protocol: [`deps/im/docs/design/gateway.md`](../../../deps/im/docs/design/gateway.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
