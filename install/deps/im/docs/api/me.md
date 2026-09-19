# Get the Current User

`GET /api/v1/me` returns the user identified by an Airway IM credential.
The plugin ships no login flow: the credential is signed by the host
application backend with the shared `IM_AUTH_SECRET` (or minted on the
host's behalf through `POST /internal/v1/credentials`). The first time a
credential authenticates, the user is registered automatically. See
[`../design/identity.md`](../design/identity.md) for the credential format.

## Request

Send the credential in the standard `Authorization` header:

```http
GET /api/v1/me HTTP/1.1
Host: api.example.invalid
Authorization: Bearer im1.<base64url-payload>.<base64url-signature>
```

For example:

```bash
curl https://api.example.invalid/api/v1/me \
  -H 'Authorization: Bearer im1.<base64url-payload>.<base64url-signature>'
```

## Successful Response

The API returns HTTP `200` with the user profile in `data`. The credential
is never echoed back.

```json
{
  "code": 0,
  "data": {
    "id": 1,
    "uuid": "shop1:user-42",
    "username": "octocat",
    "nickname": "The Octocat",
    "avatar_url": "https://avatars.example.invalid/u/583231",
    "email": "octocat@example.invalid",
    "last_seen_at": "2026-07-19T10:00:00Z",
    "created_at": "2026-07-19T10:00:00Z",
    "updated_at": "2026-07-19T10:00:00Z"
  },
  "message": null
}
```

## Invalid Credential

A missing, malformed, invalid, or expired credential returns HTTP `400`:

```json
{
  "code": 10001,
  "data": null,
  "message": "Invalid bearer token"
}
```

Treat credentials as secrets: store them securely, send them only over HTTPS,
and never place them in URLs or logs.
