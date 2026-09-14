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
| `GET` | `/admin/api/conversations` | List all group conversations with counters. |
| `GET` | `/admin/api/conversations/{id}/messages` | List all messages in a group conversation. |
| `POST` | `/admin/api/messages/{id}/mark-illegal` | Mask a message and notify online members. |

Protected operations use `Authorization: Bearer <admin-session-token>`. Admin
session tokens and user credentials are different credential types and cannot
be substituted for one another.

The plugin ships no user login flow. Users register automatically the first
time a host-signed credential authenticates (see
[`../design/identity.md`](../design/identity.md)); `GET /admin/api/users`
lists those registered identities for operational visibility.

`POST /admin/api/users/{uuid}/revoke` invalidates every credential the
backend minted for that user (by bumping `token_version`) and best-effort
kicks their live Gateway connections via `IM_GATEWAY_URL`:

```json
{"code": 0, "data": {"uuid": "user-42", "token_version": 2, "connections_kicked": 1}, "message": null}
```

An unknown `uuid` returns 404. Revocation is not a ban: the host can mint a
fresh credential at any time; to keep a user out, the host must stop minting
for them.
