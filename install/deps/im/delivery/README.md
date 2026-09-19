# Airway IM Delivery

The transactional-outbox publisher of the airway-im-plugin. Messages are
committed to the database together with an outbox event in one transaction;
this worker polls the backend for unpublished events, pushes each one to the
WebSocket gateway for real-time delivery, and acknowledges it afterwards.

Because a publisher may crash between publishing and acknowledging, events
can be delivered more than once; clients deduplicate by `event_id` and
`message_id`. Offline clients recover through the backend's sequence-based
synchronization API, not through this worker.

## Endpoints

| Path | Purpose |
| --- | --- |
| `/healthz` | Health probe |
| `/metrics` | Prometheus-style metrics |
| `/dashboard` | Human-readable metrics dashboard |

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `DELIVERY_ADDR` | `:1920` | Listen address |
| `BACKEND_URL` | `http://127.0.0.1:1906` | Backend base URL (outbox + ack) |
| `GATEWAY_URL` | `http://127.0.0.1:1910` | Gateway base URL (delivery push) |
| `IM_INTERNAL_SECRET` | — | Shared secret for backend/gateway internal endpoints; required |
| `DELIVERY_POLL_INTERVAL_MS` | `500` | Outbox polling interval (minimum 100) |

## Run

```bash
go run .            # expects backend on 127.0.0.1:1905 and gateway on 127.0.0.1:1910
```

or as a container via the shipped `Containerfile`.
