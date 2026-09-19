# airway-im-sdk-ts

The JS/TS SDK for [Airway IM](https://github.com/daqing/airway-im-plugin),
covering WeChat Mini Programs and browsers (Vue / React / plain web pages). It
wraps the backend's REST API and WebSocket gateway protocol into ready-to-use
TypeScript interfaces, so clients never have to implement credentials, the
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
- **Mini Program friendly** — the default adapter is built on
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
credential **server-to-server** — calling the internal minting endpoint
`POST /internal/v1/credentials` on the Airway project's IM service (with
`X-IM-Internal-Secret`), or signing it locally — then returns the finished
credential together with your own session token in the login response
(valid for 24 hours; mint a fresh one before expiry).

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
[`deps/im/docs/design/identity.md`](../../deps/im/docs/design/identity.md).

### 2. Initialize and exchange messages in a Mini Program

```ts
import { createIM } from "airway-im-sdk-ts";

const im = createIM({
  apiUrl: "https://im.example.com",   // IM backend (:1905), must be https
  wsUrl: "wss://im.example.com",      // WebSocket gateway (:1910), must be wss
  credential: wx.getStorageSync("im-credential"),
  getCredential: () =>                // called automatically when invalid
    fetchNewCredentialFromYourBackend(),
});

// Listen for messages: ordered, deduplicated, gap-filling (including
// messages missed while offline)
im.on("message", (msg, source) => {
  console.log(`[${source}]`, msg.sender.nickname, msg.content);
});
im.on("status", (s) => console.log("connection:", s));

im.connect();

// Opening a conversation for the first time: pull history and start
// tracking realtime sync for it
const { id } = await im.createDirect(otherUserId);
const history = await im.history(id);   // all messages, ascending sequence

// Sending (the SDK generates the Idempotency-Key automatically and retries
// network failures with the same key, so a message is never duplicated)
const sent = await im.sendMessage(id, "你好", { contentType: "text/plain" });
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

### `createIM(options)`

| Option | Required | Default | Description |
| --- | --- | --- | --- |
| `apiUrl` | ✓ | — | IM backend URL (:1905); https in production |
| `wsUrl` | | — | Gateway URL (:1910); without it only REST is used, no realtime |
| `credential` | ✓ | — | User credential issued by your backend |
| `getCredential` | | — | `() => Promise<string>`, fetches a fresh credential when invalid |
| `adapter` | | WeChat adapter | Platform adapter; pass `browserAdapter()` in browsers (see below) |
| `timeoutMs` | | `15000` | REST request timeout |
| `pingIntervalMs` | | `25000` | Application-level heartbeat interval, `0` disables |
| `persistSequences` | | `true` | Persist per-conversation sequence cursors |
| `autoConnect` | | `false` | Connect to the gateway immediately after creation |

### REST methods (`im.*`)

| Method | Endpoint |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createConversation({kind, memberIds, title?})` | `POST /api/v1/conversations` |
| `im.createDirect(otherUserId)` | Same (direct get-or-create) |
| `im.createGroup(title, memberIds)` | `POST /api/v1/group` |
| `im.getConversation(uuid)` | `GET /api/v1/conversations/:uuid` |
| `im.addMembers(conversationId, memberIds)` | `POST .../members` |
| `im.removeMember(conversationId, userId)` | `DELETE .../members/:user_id` |
| `im.history(conversationId, {fromSequence?, limit?})` | Pull history + start tracking sync |
| `im.listMessages(conversationId, {afterSequence?, limit?})` | `GET .../messages` (raw paging) |
| `im.sendMessage(conversationId, content, opts?)` | `POST /api/v1/messages` |
| `im.uploadFile(filePath \| File, dir?)` | `POST /api/v1/storage` |
| `im.storageUrl(key)` | File download URL (for `<image>` / `wx.downloadFile`) |

`sendMessage` opts: `contentType` (`text/markdown` default / `text/plain`),
`idempotencyKey` (auto-generated by default), `retries` (network-failure
retries, default 1).

### Realtime events (`im.on(name, handler)`)

| Event | Payload | Description |
| --- | --- | --- |
| `message` | `(msg, "history"\|"realtime")` | New message, ordered, deduplicated, gap-filled |
| `message.updated` | `(msg)` | Message masked by moderation; content is already `***`; re-render by replacing |
| `members.added` | `{conversationId, addedUserIds, event}` | Group members added (including the added users themselves) |
| `members.removed` | `{conversationId, removedUserId, event}` | Group member removed (a kicked user is notified too) |
| `status` | `ConnectionStatus` | `connecting / authenticating / online / reconnecting / offline / closed` |
| `error` | `Error` | Gateway auth failure, credential renewal failure, etc. |
| `event` | Raw `GatewayEvent` | Every gateway frame; messages of conversations not yet `history()`-tracked also arrive here |

Connection control: `im.connect()` (idempotent) / `im.disconnect()` /
`im.isOnline` / `im.connectionStatus` / `im.setCredential(cred)` /
`im.lastSequence(conversationId)` / `im.forgetConversation(conversationId)`
(e.g. drop the sync cursor after being kicked from a group).

### Error handling

All REST errors throw `IMError` (`err.code` — the envelope business code,
`err.status` — the HTTP status code, `status === 0` on transport failure).
Common checks:

```ts
import { IMError, ErrorCode } from "airway-im-sdk-ts";

try {
  await im.sendMessage(id, "hi");
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

## Message reliability model

The backend guarantees a monotonically increasing per-conversation
`sequence`; delivery is **at-least-once**. The SDK's sync engine takes care
of deduplication by `event_id` / `message_id`, ordering by `sequence`, and
gap-filling via `after_sequence`. Business code only needs to:

1. initialize a conversation with `im.history(id)` (start tracking);
2. append-render in the `message` event (handle repeated messages idempotently
   by `msg.id`);
3. render sends optimistically from `sendMessage`'s return value and dedupe
   the realtime echo by `id`.

Conversations never tracked through `history()` do not auto-fetch messages
(avoids flushing the whole history); handle them via the `event` event for
unread badges and similar cases.

## Mini Program domain configuration

In the WeChat MP console → Development → Development Settings → Server
Domains, configure:

- **request legal domains**: `https://im.example.com` (REST + upload)
- **socket legal domains**: `wss://im.example.com` (realtime gateway)

For local debugging, check "do not verify legal domains" in the DevTools and
use `http://127.0.0.1:1905` / `ws://127.0.0.1:1910`.

## Using in Vue / React / plain web pages

The SDK core is platform-agnostic; Mini Programs use the default
`wechatAdapter`, while browsers explicitly pass the built-in `browserAdapter`
(fetch / WebSocket / FormData / localStorage):

```ts
import { createIM, browserAdapter } from "airway-im-sdk-ts";

export const im = createIM({
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

- API contract: [`deps/im/docs/api/openapi.md`](../../deps/im/docs/api/openapi.md)
- Gateway protocol: [`deps/im/docs/design/gateway.md`](../../deps/im/docs/design/gateway.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../deps/im/docs/design/identity.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
