# Create a Chat Message

`POST /api/v1/messages` creates a message in a conversation. Clients send
message content through this HTTPS API; they do not send chat messages over the
WebSocket connection. The WebSocket Gateway is an authenticated, server-to-
client delivery channel used to notify online recipients after a message has
been committed.

This endpoint and its persistence models are implemented. The canonical schema
for client integration is maintained in [`openapi.md`](openapi.md); this guide
provides the detailed behavior and design rationale.

## Request

### Endpoint

```http
POST /api/v1/messages HTTP/1.1
Host: api.example.invalid
Authorization: Bearer im1.<base64url-payload>.<base64url-signature>
Content-Type: application/json
Idempotency-Key: 018f7d62-60d1-7d24-bfe1-3b4410f09a0a
```

The endpoint is available only over HTTPS outside local development.

### Authentication

Send the Airway-signed user credential in the standard `Authorization` header
(see [`../design/identity.md`](../design/identity.md)):

```text
Authorization: Bearer <credential>
```

The credential identifies the message sender. A client must not submit a sender ID
in the request body. Missing, malformed, invalid, or expired credentials are
rejected.

### Headers

| Header | Required | Description |
| --- | --- | --- |
| `Authorization` | yes | Airway IM Airway-signed credential; see [`../design/identity.md`](../design/identity.md). |
| `Content-Type` | yes | Must be `application/json`. UTF-8 is assumed. |
| `Idempotency-Key` | recommended | A unique, client-generated value for safely retrying one logical send operation. UUIDv7 is recommended. |

An idempotency key is scoped to the authenticated user. The client must reuse
the same key when retrying the same message and use a new key for a new message.
The server stores the key and its result for a configured retention period,
recommended at 24 hours. Reusing a key with a different request body is an
error.

### JSON body

```json
{
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "content": "Hello **Airway IM**!\n\nThis message supports Markdown.",
  "content_type": "text/markdown"
}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `conversation_id` | string | yes | Stable ID of the destination direct-message or group conversation. |
| `content` | string | yes | UTF-8 message source. For Markdown, this is the original Markdown text, not rendered HTML. |
| `content_type` | string | no | Content format. Defaults to `text/markdown`; version 1 accepts only `text/markdown` and `text/plain`. |

Unknown fields are rejected so that misspelled security- or routing-sensitive
fields do not silently pass validation.

The request body contains JSON even when the content itself is Markdown. Clients
must JSON-escape quotes, backslashes, and line breaks normally. They must not
send rendered HTML or use `multipart/form-data` for this endpoint. Attachments
should be uploaded through a separate attachment API and referenced by a future
message schema.

Example:

```bash
curl https://api.example.invalid/api/v1/messages \
  -X POST \
  -H 'Authorization: Bearer im1.<base64url-payload>.<base64url-signature>' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 018f7d62-60d1-7d24-bfe1-3b4410f09a0a' \
  --data '{
    "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
    "content": "Hello **Airway IM**!",
    "content_type": "text/markdown"
  }'
```

## Validation and authorization

Before creating a message, the API must:

1. authenticate the bearer credential;
2. require a valid JSON object and reject trailing JSON data;
3. validate the conversation ID and content type;
4. normalize line endings to `\n` without otherwise rewriting the content;
5. reject content that is empty after trimming Unicode whitespace;
6. enforce the configured UTF-8 byte and character limits;
7. verify that the authenticated user is an active member of the conversation;
8. apply per-user and per-conversation rate limits; and
9. process the idempotency key, when supplied.

The recommended initial content limit is 32 KiB after JSON decoding. The server
must measure both bytes and characters explicitly; it must not truncate a
message to make it fit.

Markdown is untrusted user input. The API stores the Markdown source and must
not execute or fetch anything referenced by it. Every client or server-side
renderer must sanitize generated HTML, disallow executable content, and apply a
safe policy to links and embedded media. Authorization must not rely on fields
provided by the client.

## Successful response

After the message and its delivery event have been committed durably, the API
returns HTTP `201 Created`:

```json
{
  "code": 0,
  "data": {
    "id": "01J2Q8A4FQ8NA8R6YDJ2M98K3P",
    "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
    "sender": {
      "id": 1,
      "username": "octocat",
      "nickname": "The Octocat",
      "avatar_url": "https://avatars.example.invalid/u/583231"
    },
    "content": "Hello **Airway IM**!\n\nThis message supports Markdown.",
    "content_type": "text/markdown",
    "created_at": "2026-07-20T10:15:30.123Z",
    "sequence": 1042
  },
  "message": null
}
```

The server assigns the canonical message ID, conversation sequence, sender, and
timestamp. Clients must replace optimistic local values with the returned
values. `sequence` is monotonically increasing within one conversation and is
used for ordering and synchronization; it is not globally ordered.

If the same idempotency key and equivalent body are submitted again, the server
returns the original message without creating or publishing a duplicate. It may
return HTTP `200 OK` for such a replay. Client logic must use `code` and the
message ID rather than relying on `201` to distinguish the first response.

## Error responses

All application errors use the common response envelope:

```json
{
  "code": 10003,
  "data": null,
  "message": "Message content is required"
}
```

| HTTP status | Code | Meaning |
| --- | --- | --- |
| `400 Bad Request` | `10001` | Missing, malformed, invalid, or expired credential, matching the existing authenticated API convention. |
| `400 Bad Request` | `10003` | Malformed JSON, unsupported content type, missing field, invalid field, or content limit exceeded. |
| `403 Forbidden` | `10005` | The authenticated user is not allowed to send to the conversation. |
| `404 Not Found` | `11001` | The conversation does not exist. A deployment may return this instead of `10005` only when revealing existence is safe. |
| `409 Conflict` | `11002` | The idempotency key was reused with a different request body. |
| `429 Too Many Requests` | `10004` | A rate limit was exceeded. The response should include `Retry-After`. |
| `500 Internal Server Error` | `10000` | An unexpected server error occurred. |
| `503 Service Unavailable` | `10006` | A required persistence or event-delivery dependency is temporarily unavailable. |

Error messages are diagnostic and may change. Clients branch on the numeric
`code`, not the `message` string. Internal errors must not expose credentials,
database details, message bodies, or stack traces.

## Persistence and real-time delivery

Creating a message and recording its outbound delivery event must use one
transactional boundary, such as a database transaction with an outbox record.
The API returns success only after that transaction commits. This prevents a
message from being acknowledged but never published after a process crash.

After commit:

1. an outbox publisher sends a message-created event to the internal event bus;
2. the routing layer identifies active recipient sessions;
3. the appropriate Gateway instances enqueue the event for their local
   WebSocket connections; and
4. offline or disconnected clients retrieve the message later through the
   conversation synchronization/history API.

The sender may also receive the event on its authenticated WebSocket
connections, including other devices. Clients deduplicate the HTTP response and
WebSocket delivery by the canonical message ID.

Delivery is at least once. WebSocket receipt does not mean the recipient has
read the message, and a successful API response does not mean every recipient
is online. Read and delivery receipts require separate APIs and event types.

## Client retry behavior

The client may retry with the same idempotency key after a timeout, connection
failure, HTTP `500`, or HTTP `503`, using exponential backoff and jitter. It must
honor `Retry-After` for HTTP `429` and must not automatically retry validation,
authorization, or idempotency-conflict errors.

Without an idempotency key, a retry after an ambiguous network failure may
create a duplicate message. For that reason, production clients should always
provide one.

## Privacy and observability

Logs and traces may include the request ID, authenticated user ID, conversation
ID, message ID, response code, payload byte length, and latency. They must not
include the bearer credential or message content. Metrics should cover request rate,
latency, response codes, rejected payload sizes, rate limiting, idempotent
replays, persistence failures, and outbox publication lag.
