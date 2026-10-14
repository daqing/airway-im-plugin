# Admin API

The admin API backs operational dashboards. It is not a client API and must
not be exposed publicly without network-level access control and TLS.

Configure credentials with `IM_ADMIN_USERNAME` and
`IM_ADMIN_PASSWORD`. Login creates an in-memory session valid for 12
hours; restarting the backend invalidates all sessions.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/admin/api/login` | Exchange administrator credentials for a session token. |
| `POST` | `/admin/api/logout` | Revoke the current session. |
| `GET` | `/admin/api/status` | Aggregate user, outbox, Gateway, and Delivery metrics. |
| `GET` | `/admin/api/users` | List automatically registered user identities. |
| `POST` | `/admin/api/users/{uuid}/revoke` | Invalidate the user's minted credentials (bumps `token_version`) and kick live Gateway connections. |
| `GET` | `/admin/api/conversations` | List conversations of every kind with counters. Optional `?kind=group` or `?kind=direct` narrows the listing; direct entries carry their `participants`. |
| `GET` | `/admin/api/conversations/{id}/messages` | List all messages in a conversation (group or direct). |
| `POST` | `/admin/api/messages/{id}/mark-illegal` | Mask a message (group or direct) and notify online members. |
| `GET` | `/admin/api/reviews` | List the pre-delivery review queue. Defaults to `?status=pending`; `approved` and `rejected` list decided entries. |
| `POST` | `/admin/api/reviews/{review_id}/approve` | Deliver a queued message, reusing the review id as the message id. |
| `POST` | `/admin/api/reviews/{review_id}/reject` | Discard a queued message, label its sender `illegal`, and notify that sender. |

Protected operations use `Authorization: Bearer <admin-session-token>`. Admin
session tokens and user credentials are different credential types and cannot
be substituted for one another.

## Review queue

A message is queued instead of delivered when the sending client reported it
as screened (`labels: ["illegal"]`), when the moderation service's word list
matches its content, or when the sender already carries the `illegal` label.
Queued messages live in `message_reviews` with status `pending`; they hold no
`sequence` and reach no member.

Each queued entry records `flag_matches`: the words the moderation service
found, so an operator can see why the message was held. When the service could
not be reached the entry holds `["screening-unavailable"]` instead — the
message is queued because nobody screened it, not because it matched. Screening
is fail-closed: while the moderation service is down every message is queued.

Approving a review releases the message into its conversation under the
review record's own id, so a client that already rendered the pending
message replaces it instead of showing a duplicate. Rejecting it discards the
message, appends `illegal` to `users.labels` (so the sender's later messages
are queued too), and pushes a `message.review_rejected` Gateway event to that
sender alone.

Both endpoints return the updated review record and are idempotent per
decision: acting twice on the same entry returns 409, an unknown id returns
404.

The plugin ships no user login flow. Users register automatically the first
time an Airway-signed credential authenticates (see
[`../design/identity.md`](../design/identity.md)); `GET /admin/api/users`
lists those registered identities for operational visibility.

`POST /admin/api/users/{uuid}/revoke` invalidates every credential the
backend minted for that user (by bumping `token_version`) and best-effort
kicks their live Gateway connections via `IM_GATEWAY_URL`:

```json
{"code": 0, "data": {"uuid": "user-42", "token_version": 2, "connections_kicked": 1}, "message": null}
```

An unknown `uuid` returns 404. Revocation is not a ban: the Airway project can mint a
fresh credential at any time; to keep a user out, the Airway project must stop minting
for them.
