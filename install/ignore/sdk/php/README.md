# airway-im-sdk-php

The PHP SDK for [Airway IM](https://github.com/daqing/airway-im-plugin): it
wraps the backend's REST API and the credential-minting endpoint into
ready-to-use PHP interfaces, so PHP projects (Laravel / Symfony / WordPress /
any PHP service) never have to implement minting calls, envelope parsing,
idempotent retries, or automatic credential renewal themselves.

Zero dependencies — PHP 8.0+ standard library only. No Composer packages are
required at runtime, and the transport is built on stream sockets, so it
works even without the cURL extension or `allow_url_fopen`.

**Positioning**: this is a **server-side SDK**. It covers every scenario in
which a PHP backend integrates with IM — minting credentials for your users
through the internal API, calling the IM API as any user, and admin
operations. For realtime WebSocket send/receive in browsers or Mini Programs,
pair your front end with the JS/TS SDK ([`airway-im-sdk-ts`](../ts/), which
includes the sync engine); a PHP side that needs to listen for new messages
(bots, notifications) can simply poll the sequence-based sync API (see
`eachMessage` below).

## Installation

With Composer:

```bash
composer require daqing/airway-im-sdk-php
```

Composer's PSR-4 autoloader picks the classes up automatically. Without
Composer, require the bundled autoloader instead:

```php
require '/path/to/airway-im-sdk-php/src/autoload.php';
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

```php
use AirwayIM\InternalClient;

$internal = new InternalClient(
    internalUrl: 'http://127.0.0.1:1906',   // internal listener, private network only
    internalSecret: getenv('IM_INTERNAL_SECRET'),
);

$minted = $internal->mintCredential(
    uuid: 'user-42',
    name: 'alice',
    nickname: 'Alice',
    ttlSeconds: 86400,
);
$credential = $minted->credential();        // "im1.…"
$minted->expiresAt();                       // ISO8601 string; null when ttlSeconds is 0
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

Every method returns the envelope's `data` (an array, or null) and throws
`AirwayIM\Error` on failure. Conversation-getters return
`AirwayIM\DirectConversation` / `AirwayIM\GroupConversation` objects — one
object per conversation carrying `->id` and `->kind` (plus `->title` on
groups), with `send` / `listMessages` / `eachMessage` scoped to it. Message
responses come back as `AirwayIM\Message`, a JSON-object wrapper —
`$message["id"]`, `$message->id`, and `$message->sender->uuid` all work:

```php
use AirwayIM\Client;

$im = new Client(
    apiUrl: 'https://im.example.com',
    credential: $credential,
    getCredential: fn () => mintFreshCredential(), // optional: auto-renew + retry once on 401/10001
);

$direct = $im->createDirect('user-2');      // DirectConversation object (get-or-create)
$im->getDirect('user-2');       // the existing object with "user-2", or null (read-only)
$group = $im->createGroup(memberUUIDs: ['user-2', 'user-3'], title: 'Backend Team');
$im->listGroups();                          // groups I belong to
$group->details();                           // conversation kind + members with roles
$im->openConversation($group->id);          // any conversation by id, as a conversation object

// Send a message: the Idempotency-Key is generated automatically and reused
// across network-failure retries, so a message is never duplicated
$direct->send('你好');
$message = $im->sendDirectMessage('user-2', '你好'); // one call: get-or-create the direct conversation, then send
$message->sequence;                         // Message supports property reads; $message['sequence'] works too

$group->addMembers(['user-4']);             // group-only operations live on the object
$group->removeMembers(['user-4']);
$group->details();                          // members with roles, fresh from the API

// History / sequence sync (catch up from the last cursor after going offline,
// returned in ascending order)
$im->listMessages($direct->id, afterSequence: 42, limit: 100);
foreach ($im->eachMessage($direct->id) as $message) { … } // auto-paging over the full history

$im->uploadFile('/path/to/avatar.png', dir: 'avatars');   // returns {key, url, size}
$im->storageUrl('avatars/202609/xxx.png');                // download URL
```

A service account (bot / system notifications) follows the same path: give
the bot a fixed `(uuid, name)`, mint a long-lived credential for it, and use
it to create conversations and send messages.

## API reference

### `AirwayIM\Client` (public API, default `:1905`)

Method names mirror the TS SDK one to one (camelCase both sides). The same
conversation objects exist here — `AirwayIM\DirectConversation` / `AirwayIM\GroupConversation`
with `send` / `listMessages` / `eachMessage` (groups: `addMembers` /
`removeMembers`) and `openConversation(id)` — minus the realtime
layer: the TS objects' `on("message")` subscriptions, gateway, and sync
engine have no PHP counterpart; poll `eachMessage` instead.

| Method | Endpoint |
| --- | --- |
| `me()` | `GET /api/v1/me` |
| `listGroups()` | `GET /api/v1/conversations?type=group` |
| `createDirect(otherUserUUID)` | Same (direct get-or-create), as a `DirectConversation` object |
| `getDirect(otherUserUUID)` | `GET /api/v1/conversations/direct/:user_uuid` (object; null when none) |
| `createGroup(title: null, memberUUIDs)` | `POST /api/v1/group` (as a `GroupConversation` object) |
| `openConversation(conversationId)` | `GET /api/v1/conversations/:uuid`, returned as a conversation object (kind/title resolved from the details) |
| `addMembers(conversationId, memberUUIDs)` | `POST .../members` (owner/admin, idempotent) |
| `removeMembers(conversationId, userUUIDs)` | `DELETE .../members/:user_uuid` (owner/admin) |
| `listMessages(conversationId, afterSequence: null, limit: null)` | `GET .../messages` (raw paging, limit 1–200) |
| `eachMessage(conversationId, afterSequence: 0, pageSize: 100)` | Auto-paging generator over the history, ascending |
| `sendGroupMessage(conversationId, content, contentType:, idempotencyKey:, retries:)` | `POST /api/v1/messages` |
| `sendDirectMessage(otherUserUUID, content, …)` | Get-or-create the direct conversation, then `POST /api/v1/messages` |
| `uploadFile(path or stream, filename:, dir:)` | `POST /api/v1/storage` (multipart) |
| `storageUrl(key)` | File download URL |

Constructor options: `apiUrl` (required), `credential` (required),
`getCredential` (optional callable — on `10001`/401 automatically fetches a
fresh credential and retries once; the `credential` property is updated),
`timeout` (seconds, default 15). `contentType` accepts `text/markdown`
(default) and `text/plain`; content is limited to 32768 bytes and the server
normalizes CRLF to LF.

### Conversation objects

`createDirect` and `getDirect` return an
`AirwayIM\DirectConversation`; `createGroup` and
`openConversation` resolve to an `AirwayIM\GroupConversation` (or
`DirectConversation`) using the details lookup. Every object carries `->id`
and `->kind` and scopes the conversation API to itself:

| Handle method | Delegates to |
| --- | --- |
| `send(content, contentType:, idempotencyKey:, retries:)` | `POST /api/v1/messages` |
| `listMessages(afterSequence:, limit:)` | `listMessages(id, …)` |
| `eachMessage(afterSequence: 0, pageSize: 100)` | `eachMessage(id, …)` |
| `addMembers(memberUUIDs)` — groups | `addMembers(id, memberUUIDs)` |
| `removeMembers(userUUIDs) — groups | `removeMembers(id, userUUIDs)` |
| `details()` | `GET /api/v1/conversations/:uuid` |

`$group->title` carries the creation-time title (`null` when created without
one or when opened by id); `details()` returns the live value.

### Message object

`sendGroupMessage`, `sendDirectMessage`, `listMessages`, and `eachMessage` return
`AirwayIM\Message` objects — JSON objects with property reads, so
`$message["id"]`, `$message->id`, and `$message->sender->uuid` all work, and
`json_encode($message)` serializes back to the API payload:

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
| `sequence` | integer ≥ 1 | Position within the conversation, allocated at commit. `(conversation_id, sequence)` is the total order to sort by; pass the last seen value as `afterSequence` to page history. |

Sort by `sequence`, never by `created_at`. A retried send with the same
`Idempotency-Key` returns the original message (same `id` and `sequence`), so
retries never create a second local entry. `sender` reflects the author's
current profile, not a send-time snapshot.

### `AirwayIM\Credentials` (signing and verification helpers)

Local signing with `sign` is for trusted holders of `IM_AUTH_SECRET` on the
Airway project's own side; third-party platform backends never hold that
secret and must use `InternalClient::mintCredential` instead.

| Method | Description |
| --- | --- |
| `sign(secret, uuid, name, nickname: null, avatarUrl: null, tokenVersion: null, ttl: 86400, now: null)` | Issues an `im1.<payload>.<sig>` HMAC-SHA256 credential; optional fields are written only when present, never clobbering an existing profile; `now` is an injectable unix-seconds clock |
| `decode(credential)` | Returns the claims (throws `Error` on malformed input) |
| `verifySignature(credential, secret)` | Constant-time verification, returns `false` on malformed input |
| `expired(credential or claims, now: null)` | Whether `exp` has passed; credentials without `exp` never expire |

### `AirwayIM\InternalClient` (internal minting endpoint, default `127.0.0.1:1906`)

| Method | Description |
| --- | --- |
| `mintCredential(uuid, name, nickname: null, avatarUrl: null, ttlSeconds: null)` | Server-to-server credential minting; `ttlSeconds` defaults to 86400, capped at 2592000, `0` omits expiry; returns a `MintedCredential` (`credential()`, `expiresAt()`) |

### `AirwayIM\AdminClient` (admin console, `/admin/api`)

Logs in automatically on the first call (12-hour session); a mid-session 401
triggers one re-login and retry.

| Method | Description |
| --- | --- |
| `login` / `logout` | Explicit login / logout |
| `status` | Aggregated status: user count, online users, gateway/delivery metrics |
| `users` | Registered users (with `token_version`) |
| `revokeUser(uuid)` | Revoke all of a user's credentials and kick their connections |
| `groupConversations` | All group conversations (with member/message counts) |
| `conversationMessages(conversationId)` | Group message review (ascending) |
| `markIllegal(messageId)` | Mark illegal (idempotent): content masked to `***` for clients |

## Error handling

All errors throw `AirwayIM\Error` (`getCode()` — the envelope business code,
`->status` — the HTTP status code, `status == 0` and `code == -1` on
transport failure):

```php
use AirwayIM\Error;
use AirwayIM\ErrorCode;

try {
    $im->sendGroupMessage($id, 'hi');
} catch (Error $e) {
    if ($e->authError()) {                                     // 10001 / 401: credential invalid or expired — renew or re-mint
    } elseif ($e->getCode() === ErrorCode::PERMISSION_DENIED) {      // 10005
    } elseif ($e->getCode() === ErrorCode::CONVERSATION_NOT_FOUND) { // 11001
    }
}
```

Full error codes: `10000` internal error, `10001` invalid credential,
`10003` invalid request, `10005` permission denied, `11001` conversation not
found, `11002` idempotency key reused with a different request.

## Message reliability model

The backend guarantees a monotonically increasing per-conversation
`sequence`; delivery is **at-least-once**. Recommended practice for
server-side integrations:

1. Catch up with `listMessages($uuid, afterSequence: $lastCursor)` or
   `eachMessage`;
2. Process idempotently by `$message['id']` and order by `sequence`;
3. Treat `sendGroupMessage`'s return value as authoritative — retries are handled
   by the SDK (the same `Idempotency-Key` guarantees no duplicates).

Realtime reception and automatic gap-filling for end-user clients (Mini
Programs / browsers) belong to [`airway-im-sdk-ts`](../ts/); the PHP polling
interval is simply your business latency requirement.

## Concurrency

`Client` / `InternalClient` / `AdminClient` instances are safe to reuse
across requests and worker tasks: every request uses an independent
connection, and credential refresh / login are one-shot overwrites. Serialize
`AdminClient::login` / `logout` against business calls yourself if they run
concurrently.

## Local development and verification

```bash
php test/run.php   # stdlib-only test runner: signing vectors, envelope parsing, idempotent retry, credential renewal, multipart, chunked responses…
```

The tests ship a data-driven stub HTTP server on real sockets (no PHPUnit,
no extensions) and exercise real request/response I/O, including transport
retries and read timeouts. For end-to-end verification against a real stack
(backend :1905 / internal :1906, see the repository root README): start the
stack and run `php test/e2e.php`, or follow "Quick start" step by step;
credential-signing correctness is pinned by a cross-language vector from the
Node.js reference implementation (`test/credentials_test.php`).

## Protocol reference

- API contract: [`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- Credential issuance: [`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- Admin console: [`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

中文版本：[README.zh-CN.md](README.zh-CN.md)
