# Airway IM Gateway

The WebSocket gateway of the airway-im-plugin. It maintains long-lived client
connections, authenticates them against the backend's internal API, and
delivers real-time IM events pushed by the delivery service.

The gateway is horizontally scalable and disposable: it keeps only live
sockets and bounded outbound queues for its own connections. It never talks to
the database and contains no conversation business logic.

## Endpoints

| Path | Purpose |
| --- | --- |
| `/ws` | Client WebSocket endpoint (see the wire protocol below) |
| `/internal/v1/deliver` | Delivery-service push endpoint (shared-secret protected) |
| `/healthz`, `/readyz` | Health and readiness probes |
| `/metrics` | Prometheus-style metrics |
| `/dashboard` | Human-readable metrics dashboard |

## Wire protocol

All application messages are UTF-8 JSON text frames. Clients authenticate with
the token issued by the backend as the first command:

```json
{"cmd":"auth","opts":["<bearer token>"]}
```

The gateway replies `{"code":0,"data":"OK","message":null}` on success and
closes the socket with `1008` after an invalid, missing, or timed-out attempt.
After authentication, server events (for example `message.created`) arrive as
unsolicited JSON frames; clients respond to protocol ping frames
automatically. See `deps/im/docs/design/gateway.md` in the repository root for the
full protocol, limits, and delivery semantics.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `GATEWAY_ADDR` | `:1910` | Listen address |
| `BACKEND_URL` | `http://127.0.0.1:1905` | Backend base URL for token authentication |
| `IM_INTERNAL_SECRET` | — | Shared secret for the internal deliver endpoint; required |
| `GATEWAY_ALLOWED_ORIGINS` | — | Comma-separated `Origin` allowlist for browser clients |

## Run

```bash
go run .            # expects the backend on 127.0.0.1:1905
```

or as a container via the shipped `Containerfile`.
