# WebSocket Gateway Design

## 1. Purpose and scope

The Airway IM WebSocket Gateway maintains long-lived client connections and
delivers real-time IM events. It is the boundary between untrusted clients and
the internal messaging services.

This document defines the target architecture and wire protocol. The current
`app/websocket` package is an in-process broadcast prototype; it does not yet
implement the authentication, distributed routing, or delivery controls
described here.

The Gateway is responsible for:

- accepting secure WebSocket connections;
- authenticating every connection with the Airway-signed credential (see
  [`identity.md`](identity.md));
- tracking which user and device are connected to which Gateway instance;
- validating commands and forwarding accepted operations to internal services;
- delivering server events to locally connected clients;
- enforcing connection, message-size, rate, and backpressure limits; and
- exposing health, readiness, metrics, and structured logs.

The Gateway must not contain conversation business logic or use its local
memory as the durable message store. Message persistence, membership checks,
fan-out decisions, unread state, and offline synchronization belong to internal
services.

## 2. Public endpoints and discovery

Production connections use TLS and the `/ws` endpoint:

```text
wss://ws0.gateway.example.invalid/ws
wss://ws1.gateway.example.invalid/ws
wss://ws2.gateway.example.invalid/ws
...
wss://ws{n}.gateway.example.invalid/ws
```

Each numbered hostname represents a Gateway shard or pool, not necessarily one
machine. A load balancer may place multiple stateless Gateway instances behind
each hostname. Capacity is increased by adding instances to an existing pool or
by publishing additional numbered hostnames.

The client must not guess an unbounded sequence of hostnames. After
authentication (or credential issuance), the API should return a short,
ordered or randomized list of currently available Gateway URLs in its
session/bootstrap response. DNS records should
use low enough TTLs to support capacity changes. A client may cache the last
successful URL, but it must try other returned URLs when connection attempts
fail. This gives the service control over draining, regional placement, and
future topology changes while retaining the `ws{n}` naming convention.

An L4 or L7 load balancer terminates TLS or passes it to the Gateway. It must
support WebSocket upgrades and idle connections. Sticky sessions are not
required: a connection remains on one instance for its lifetime, and a
reconnection may land on any healthy instance.

Only `wss://` is allowed outside local development. The credential must not be
placed in the URL or written to access logs.

## 3. High-level architecture

```text
Client
  |  WSS
  v
DNS / Load Balancer
  |
  +----> Gateway instance A ----+
  +----> Gateway instance B ----+----> Event bus / message broker
  +----> Gateway instance C ----+             |
          |                                    v
          +----> Auth / User service     Messaging services
          +----> Session registry        Durable message store
```

Gateway instances are horizontally scalable and disposable. Each instance
keeps only the live socket objects and bounded outbound queues for its own
connections. Shared coordination uses the following components:

- **Authentication source:** validates the Airway IM Airway-signed credential
  (see [`identity.md`](identity.md)) and resolves it to an internal user ID. A
  short-lived cache may reduce auth-service load, but cached verifications
  must expire quickly; credentials are stateless and become invalid only by
  expiring or by rotating the signing secret.
- **Session registry:** stores ephemeral mappings such as
  `user ID -> gateway instance ID and connection ID`. Entries have a TTL and are
  refreshed by heartbeats. The registry supports presence and targeted routing,
  but is not a source of message history.
- **Event bus:** transports commands and delivery events between Gateway
  instances and messaging services. Redis Streams, NATS JetStream, or Kafka are
  suitable depending on the required durability and throughput; the chosen
  implementation must support consumer recovery and bounded retention.
- **Durable message store:** owns message IDs, conversation order, history,
  unread state, and offline catch-up.

Every Gateway instance has a unique instance ID and registers its readiness and
capacity. Internal events carry the destination user or connection IDs.
Gateways consume only events relevant to their local connections, using
per-instance subjects/streams or a routing tier. Broadcasting every event to
every Gateway does not scale and is prohibited as the primary routing design.

## 4. Connection lifecycle

### 4.1 Establishment

1. The client obtains an Airway-signed credential from the Airway backend (directly
   signed with the shared secret, or minted via the backend's internal
   credentials endpoint; see [`identity.md`](identity.md)).
2. The client chooses a URL from the bootstrap-provided Gateway list.
3. The client performs an HTTPS WebSocket upgrade on `/ws`.
4. The Gateway accepts the socket in an **unauthenticated** state and starts a
   short authentication deadline, recommended at 10 seconds.
5. The first application message must be the `auth` command.
6. On success, the Gateway binds the connection to the resolved user and
   registers the session. Only then may the client send other commands or
   receive user events.

### 4.2 Authentication

The client sends the credential as the first element of `opts`:

```json
{"cmd":"auth","opts":["im1.<base64url-payload>.<base64url-signature>"]}
```

The word `Bearer` is not included in `opts[0]`; the credential itself is the
bearer instrument. The Gateway forwards the credential to the backend's
authentication endpoint — the same source of truth as authenticated HTTP APIs —
and associates the resulting user ID with the connection.

Successful authentication returns:

```json
{"code":0,"data":"OK","message":null}
```

If authentication fails, the Gateway may send the following diagnostic response
and must then close the socket:

```json
{"code":10001,"data":null,"message":"Invalid bearer token"}
```

Use WebSocket close code `1008` (policy violation) after an invalid, missing,
repeated, or timed-out authentication attempt. The error response is
best-effort; clients must treat the close itself as an authentication failure.
The server must never log the credential or echo it in an error.

### 4.3 Active connection

After authentication, the connection enters the **active** state. A connection
has exactly one authenticated user identity. Re-authentication on the same
socket is not allowed. Multiple connections per user are allowed so that a user
can use multiple devices; a configurable per-user and per-IP limit prevents
abuse.

The server sends WebSocket ping control frames periodically, recommended every
30 seconds, and closes a connection if no pong is received within the timeout,
recommended at 10 seconds. Any application-level heartbeat added later must use
a documented command and must not replace protocol-level ping/pong handling.

### 4.4 Disconnect and reconnect

On disconnect, the Gateway removes the local connection and its session-registry
entry. TTL expiration cleans up entries left by a crashed instance. Presence
should use a short grace period to avoid rapid online/offline changes during a
reconnect.

Clients reconnect with exponential backoff and jitter, for example from 1
second up to 30 seconds. They should rotate through the bootstrap Gateway list
instead of repeatedly selecting a failed endpoint. Authentication errors must
not be retried indefinitely; the client should obtain a fresh credential from
the Airway backend or return to login. After reconnecting, the client requests missed messages from the
durable synchronization API using its last acknowledged cursor. The WebSocket
connection alone does not guarantee recovery of events sent while offline.

During planned shutdown, an instance stops accepting upgrades, becomes
unready, and drains existing connections for a bounded interval before closing
them. Clients then reconnect to another healthy instance.

## 5. JSON wire protocol

All application messages are UTF-8 JSON text frames. Binary frames are rejected
unless a future protocol version explicitly defines them.

### 5.1 Client command envelope

```json
{"cmd":"xxx","opts":["...","...","..."]}
```

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cmd` | string | yes | Command name. Names are lowercase ASCII and versioned by the protocol specification. |
| `opts` | array | yes | Positional command arguments. Their number and JSON types are defined per command. |

Unknown top-level fields should be ignored for forward compatibility. Unknown
commands, malformed JSON, wrong argument counts, and incorrect argument types
receive an error response. Repeated malformed or abusive input causes the
connection to be closed.

The positional `opts` format is the version 1 compatibility contract. New
commands should keep option ordering stable. If commands become complex, a
future protocol version should introduce an object-based payload rather than
overloading the positional array.

### 5.2 Server response envelope

```json
{"code":0,"data":"OK","message":null}
```

| Field | Type | Description |
| --- | --- | --- |
| `code` | integer | `0` means success; non-zero values identify protocol or application errors. |
| `data` | any or null | Command-specific response payload. |
| `message` | string or null | Human-readable diagnostic text, not intended for programmatic branching. |

Recommended common codes are:

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `10000` | Internal server error |
| `10001` | Invalid or expired credential |
| `10002` | Authentication required |
| `10003` | Invalid command or arguments |
| `10004` | Rate limit exceeded |
| `10005` | Permission denied |
| `10006` | Service temporarily unavailable |

Once multiple commands may be in flight concurrently, the protocol must add a
client-generated request ID to commands and echo it in responses. Until then,
the Gateway must process commands sequentially per connection so responses are
unambiguous. Unsolicited server events must have a separately documented event
envelope before they are introduced; they must not be confused with command
responses.

### 5.3 Protocol limits

Initial production limits should be configurable and conservative:

- authentication deadline: 10 seconds;
- maximum JSON text message: 64 KiB;
- maximum command rate: 20 messages per second with a bounded burst;
- maximum queued outbound data per connection: 256 messages or 1 MiB,
  whichever is reached first; and
- maximum connections per user and per source IP: deployment-specific.

Limits are starting points and must be tuned from production measurements. The
Gateway parses one complete WebSocket message as one JSON document. It rejects
multiple JSON documents in a frame and safely handles fragmented WebSocket
frames through the WebSocket library.

## 6. Message routing and delivery semantics

### Client-to-service flow

1. The Gateway parses the envelope and validates authentication, schema, size,
   rate, and basic authorization context.
2. It attaches trusted metadata such as user ID, connection ID, instance ID,
   receive timestamp, and trace ID. Client input cannot override this metadata.
3. It publishes the command to the appropriate internal service.
4. The service validates conversation membership, commits durable state, assigns
   the canonical message ID and ordering key, and emits result and delivery
   events.
5. The Gateway returns the command result and routes events to connected
   recipients.

### Service-to-client flow

1. The messaging service determines recipient user IDs after the durable write.
2. The routing layer resolves their active Gateway sessions.
3. An event is published to each relevant Gateway instance.
4. Each Gateway enqueues the event only for its matching local connections.
5. Offline users retrieve the event later through the synchronization/history
   path.

Delivery across the broker and reconnect boundaries is **at least once**.
Clients must deduplicate by stable message or event ID. The durable store, not
socket arrival time, defines conversation ordering. Backpressure queues are
bounded: a slow consumer must not block other connections. When a queue is
full, disconnect the slow client and let it recover via synchronization rather
than growing memory without limit.

## 7. Concurrency model

Each connection has one read loop and one write loop. Application handlers and
broker consumers never write directly to a socket; they enqueue serialized
messages into that connection's bounded outbound queue. This preserves frame
ordering and satisfies WebSocket libraries that permit only one concurrent
writer.

The process maintains indexed local maps for connection ID and user ID. Access
is synchronized, but network writes, broker calls, database calls, and JSON
encoding must never occur while holding a global connection-map lock. Context
cancellation stops all work associated with a closed socket.

## 8. Security

- Require modern TLS and validate the WebSocket `Origin` header against an
  allowlist for browser clients. Native clients may omit `Origin`; this does not
  replace authentication.
- Treat user credentials as secrets. Never include them in URLs, metrics,
  traces, or logs. Credentials are stateless HMAC signatures; verification
  stores nothing per credential (see [`identity.md`](identity.md)).
- Validate credential expiry. There is no per-credential revocation; rotating
  the signing secret invalidates all outstanding credentials at once.
- Perform authorization in the owning service for every conversation or message
  operation; a valid connection is not blanket permission.
- Enforce upgrade, connection, command, and payload limits at both the edge and
  Gateway. Reject compression unless its memory and denial-of-service impact is
  explicitly controlled.
- Use authenticated, encrypted service-to-service connections and least-
  privilege credentials for the registry, broker, and databases.
- Validate all JSON types and lengths before allocation or downstream
  publication.

## 9. Availability and failure handling

- Load balancers route new connections only to ready instances.
- A Gateway is not ready until its authentication dependency, session registry,
  and broker paths are usable. Liveness checks only confirm that the process is
  not deadlocked.
- A registry outage may prevent new authenticated sessions; existing sockets
  can continue only if routing correctness is preserved.
- A broker outage must produce a retryable error for new commands. The Gateway
  must not claim success before the owning service confirms its durable commit.
- A Gateway crash loses sockets and in-memory queues but not durable messages.
  Clients reconnect and synchronize.
- Deployments use connection draining and capacity headroom. Autoscaling should
  consider active connections, memory, event-loop or goroutine pressure,
  outbound queue depth, message rate, and CPU—not CPU alone.
- Multi-region deployments should return region-appropriate Gateway URLs and
  keep user routing region-aware. Cross-region delivery remains the event bus
  and messaging layer's responsibility.

## 10. Observability

Expose at least the following metrics by instance and pool:

- active, accepted, authenticated, rejected, and closed connections;
- authentication latency and failures by safe reason category;
- inbound and outbound messages and bytes;
- command latency and error counts by command and code;
- outbound queue depth, slow-consumer disconnects, and dropped events;
- broker publish/consume latency, errors, and consumer lag;
- session-registry operations and failures; and
- ping/pong latency and reconnect rate.

Structured logs include timestamp, instance ID, connection ID, user ID after
authentication, command, result code, close code, latency, and trace ID. They
exclude credentials and private message bodies. Distributed traces propagate
from the Gateway through the broker and internal services. Alerts should cover
authentication spikes, abnormal disconnect rates, saturation, broker lag, and
regional availability.

## 11. Deployment and capacity planning

Gateway instances should be deployed as identical containers with no local
durable state. Resource limits, file-descriptor limits, TCP keepalive, load-
balancer idle timeouts, and graceful-shutdown periods must be configured for
the expected connection count. Capacity tests should measure idle connection
cost, authentication bursts, fan-out bursts, slow clients, reconnect storms,
and broker degradation.

Adding `ws{n}` pools is an operational action:

1. provision and health-check the new pool;
2. publish its DNS record and TLS certificate coverage;
3. add its URL to the bootstrap response gradually;
4. monitor connection distribution and saturation; and
5. remove a pool from bootstrap before draining and removing its DNS record.

## 12. Implementation phases

1. **Protocol foundation:** strict JSON decoding, authentication deadline,
   connection state machine, single reader/writer loops, ping/pong, limits, and
   focused tests.
2. **Identity and sessions:** shared credential validation, connection/user IDs,
   multi-device policy, distributed session registry, and secret-rotation
   handling.
3. **Distributed messaging:** event-bus integration, targeted per-instance
   routing, durable service acknowledgements, event IDs, and offline sync.
4. **Production readiness:** TLS/origin policy, rate limiting, graceful drain,
   dashboards, alerts, load tests, and failure-injection tests.
5. **Protocol evolution:** request IDs, a formal server-event envelope, protocol
   negotiation/versioning, and documented command schemas.

## 13. Acceptance criteria

The first production-ready version is complete when:

- unauthenticated clients cannot execute commands or receive user events;
- invalid or timed-out authentication closes the socket;
- the documented `auth` request and success response work exactly as specified;
- users can reconnect through a different Gateway instance without losing
  durable messages;
- events are routed only to instances with intended recipients;
- slow clients and malformed or excessive input cannot cause unbounded memory
  growth;
- instances drain cleanly and stale session entries expire after crashes; and
- cross-instance routing, reconnect recovery, credential rotation, and load
  limits are covered by automated integration and capacity tests.
