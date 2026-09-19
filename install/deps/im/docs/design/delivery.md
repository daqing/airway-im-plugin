# Message Delivery System Design

## 1. Purpose and scope

The message delivery system moves a durably committed message from the
messaging API to currently connected clients. It must support direct messages,
ordinary groups, and large or high-traffic groups without creating one durable
delivery record or one broker message for every conversation member.

The design separates three concerns:

- the message store is the source of truth for message history and ordering;
- the delivery system provides low-latency notification to online sessions;
- the synchronization API recovers messages missed while a client was offline,
  disconnected, or unable to keep up.

WebSocket delivery is an optimization for low latency, not the durable copy of
a message. If 10,000 WebSocket connections must receive a group message, the
network ultimately requires up to 10,000 socket writes. The scalable design
avoids turning those writes into 10,000 database rows, queue records, routing
lookups, or offline jobs.

## 2. High-level architecture

```text
POST /api/v1/messages
        |
        v
Message service
  Database transaction:
  - validate membership
  - allocate conversation sequence
  - insert message
  - insert outbox event
        |
        v
Outbox publisher
        |
        v
Durable message event log
partition key = conversation_id
        |
        v
Delivery router
        |
        +----> Gateway A ----> local WebSocket connections
        +----> Gateway B ----> local WebSocket connections
        +----> Gateway C ----> local WebSocket connections

Offline clients ----> synchronization/history API
```

For example, consider a 10,000-member group with 3,000 online users, 1.2
connected devices per online user, and sessions spread across 30 Gateway
instances. One new message should normally produce:

- one durable message row;
- one outbox event;
- one event in the durable message log;
- at most one routed event per relevant Gateway, or one conversation event
  delivered by the broker to the relevant Gateways; and
- approximately 3,600 unavoidable WebSocket writes.

The 7,000 offline members receive no real-time delivery jobs. They synchronize
from durable history when they reconnect.

## 3. Persistence and transactional outbox

`POST /api/v1/messages` performs the following operations in one database
transaction:

1. authenticate the sender and validate active conversation membership;
2. atomically allocate the next conversation `sequence`;
3. insert the canonical message;
4. insert a `message.created` outbox event; and
5. commit the transaction.

The API returns success only after the transaction commits. It must not commit
the message and then directly publish to a broker, because a process crash
between those operations could acknowledge a message that is never delivered.

An independent outbox publisher reads unpublished rows and writes them to the
durable event log. Publication failures are retried. Since a publisher may
crash after publishing but before recording success, duplicate events are
expected and consumers must be idempotent.

A message-created event contains identifiers and ordering metadata rather than
credentials or an independently mutable copy of membership:

```json
{
  "event_id": "01K0EVENT00000000000000000",
  "message_id": "01K0MESSAGE000000000000000",
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "sequence": 182736,
  "sender_id": "01J2USER000000000000000000",
  "created_at": "2026-07-21T08:00:00Z"
}
```

The canonical message payload remains in the message store. An event may carry
the immutable delivery payload when doing so measurably reduces database reads,
provided sensitive content is protected by the broker's access controls and
retention policy.

## 4. Two delivery tiers

Reliable event processing and transient WebSocket routing have different
requirements and should be treated as separate tiers.

### 4.1 Durable message event log

The first tier records committed domain events and supports consumer recovery,
bounded retention, replay, and horizontal consumer groups. Kafka, Pulsar, or
NATS JetStream are suitable choices.

Events are partitioned by `conversation_id`. All events for one conversation
therefore reach the same partition and can be processed in conversation order.
Ordering is not defined across conversations.

### 4.2 Real-time Gateway bus

The second tier targets the Gateway instances that currently hold relevant
connections. Possible subjects include:

```text
gateway.delivery.{gateway_instance_id}
conversation.live.{conversation_id}
```

NATS is a suitable implementation because it supports high fan-out and dynamic
subjects. This tier does not need to retain events indefinitely. If a Gateway
crashes or a client is disconnected, the client recovers through durable
history.

Kafka can be used for the durable tier, but a single consumer group must not be
used as if it were Gateway broadcast: within a consumer group, only one
consumer receives each record. Gateway delivery instead requires per-instance
routing, an explicit routing tier, or broker subjects whose subscription
semantics deliver to every interested Gateway.

## 5. Hybrid fan-out strategy

No single fan-out strategy is optimal for both direct messages and large,
high-traffic groups. The delivery router uses targeted user fan-out for small
conversations and conversation subscriptions for large or hot conversations.

### 5.1 Targeted user fan-out

For a direct message or ordinary group, the router:

1. reads the active conversation members;
2. resolves their active sessions in a batch;
3. groups online users and connections by Gateway instance; and
4. publishes one batched command to each relevant Gateway.

An example Gateway command is:

```json
{
  "event_id": "01K0EVENT00000000000000000",
  "message_id": "01K0MESSAGE000000000000000",
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "sequence": 182736,
  "targets": {
    "user_ids": ["user-1", "user-7", "user-9"]
  }
}
```

Several recipients on one Gateway are represented by one broker event. The
Gateway resolves those user IDs against its local connection index and sends to
all matching devices.

This strategy is appropriate for direct conversations, groups with tens or
hundreds of members, and low-traffic groups where an occasional membership scan
is inexpensive.

### 5.2 Conversation subscription fan-out

Scanning every member for every message becomes expensive for a large or hot
conversation. For those conversations, every Gateway maintains a local index:

```text
conversation_id -> local connection set
```

When the first eligible local connection for a conversation appears, the
Gateway subscribes to `conversation.live.{conversation_id}`. When the last
eligible local connection disappears, it unsubscribes. A new message is
published once to the conversation subject. The broker forwards it only to
Gateway instances with an active subscription, and each Gateway fans it out
through its local index.

Conversation subscriptions are routing hints, not authorization grants. The
trusted conversation service owns membership. Join, leave, removal, ban, and
visibility changes must produce control events that update Gateway indexes.
Gateways must reconstruct indexes from trusted state after restart or
reconnection. Clients cannot subscribe themselves to arbitrary conversation
subjects.

### 5.3 Strategy selection

An initial implementation may use a configurable threshold such as 500 active
members:

```text
active members < threshold  -> targeted user fan-out
active members >= threshold -> conversation subscription fan-out
```

Production selection should also consider online members, message rate, number
of relevant Gateways, membership-query latency, and broker traffic. A
10,000-member group that receives one message per day has different costs from
a 2,000-member group receiving dozens of messages per second.

The strategy may change over time, but transitions must avoid missing events.
During a transition, the router can briefly publish through both paths and rely
on `event_id` deduplication.

## 6. Session registry and routing identity

Bearer tokens are authentication credentials, not routing keys. A Gateway
validates the token when a connection authenticates, resolves it to the
internal `user_id`, and never includes the token in delivery events, subjects,
logs, metrics, or traces.

The distributed session registry stores ephemeral mappings such as:

```text
session:{connection_id}
  user_id
  gateway_instance_id
  device_id
  region
  expires_at

user_sessions:{user_id}
  connection_id -> gateway_instance_id
```

Redis Cluster is one possible implementation. Entries have a TTL refreshed by
Gateway heartbeats so sessions left by a crashed instance expire automatically.
Batch reads or pipelining are required when resolving multiple users.

Each Gateway also maintains in-memory indexes for its own connections:

```text
connection_id -> connection
user_id -> local connection set
conversation_id -> local connection set
conversation_id -> local subscription reference count
```

Only live socket objects and bounded queues are stored locally. The registry is
not a source of message history or authorization truth.

## 7. Offline synchronization and unread state

The system must not create one durable offline queue entry for every member of
a group. That model produces write amplification proportional to
`messages * members`.

Messages are stored once by conversation. Relevant state can be represented by
records such as:

```text
messages(conversation_id, sequence, ...)
conversation_members(user_id, conversation_id, joined_sequence, left_sequence, ...)
user_conversation_state(user_id, conversation_id, last_read_sequence, ...)
```

After reconnecting, a client requests messages after its last durable cursor:

```http
GET /api/v1/conversations/{conversation_id}/messages?after_sequence=182700
```

If a client has sequence `182700` and receives `182703` over WebSocket, it
detects the gap and fetches the missing range. A user-level synchronization
cursor or conversation inbox version may later make multi-conversation catch-up
more efficient, but it should not require permanent per-recipient copies of
every group message.

Unread counts should normally derive from the conversation's latest visible
sequence and the member's last-read sequence. Membership boundaries and
visibility policy may require `joined_sequence`, `left_sequence`, or other
visibility ranges. Updating 10,000 unread-counter rows synchronously for every
group message is prohibited.

Push notifications are a separate asynchronous pipeline. They may require
per-user decisions, but should use batching, rate limits, collapse keys, and
notification preferences instead of blocking WebSocket delivery.

## 8. Gateway delivery model and backpressure

Each WebSocket connection has one read loop, one write loop, and a bounded
outbound queue. Broker consumers and application handlers never write directly
to a socket; they enqueue an event for the connection's writer.

```text
broker event
    -> Gateway local index
    -> connection outbound queue
    -> single writer loop
    -> WebSocket
```

Initial configurable limits should match the Gateway design:

- no more than 256 pending messages per connection;
- no more than 1 MiB of queued outbound data per connection; and
- disconnect a slow connection when either bound is reached.

The client then reconnects and synchronizes missed messages. The Gateway must
not grow memory without limit in an attempt to preserve transient delivery.

For high fan-out events, the Gateway should serialize an immutable event once
and share the encoded bytes among local outbound queues. Queues should hold
references instead of copying the full payload for every connection. Network
writes, broker calls, database operations, and serialization must not occur
while holding a global connection-index lock.

## 9. Delivery, ordering, and idempotency semantics

The system provides the following semantics:

- a message is durably stored before the API reports success;
- `(conversation_id, sequence)` defines canonical conversation order;
- messages from different conversations have no global order;
- broker and WebSocket delivery is at least once;
- clients deduplicate messages by stable `message_id` and events by `event_id`;
- WebSocket events may be duplicated, delayed, or missed during disconnection;
- clients use `sequence` to reorder events, detect gaps, and synchronize;
- an HTTP success does not mean every recipient is online or has received the
  message; and
- a successful WebSocket write does not mean the user has read the message.

The HTTP endpoint accepts an idempotency key so a client can safely retry an
ambiguous request. The outbox publisher and delivery consumers separately use
`event_id` for idempotency. Exactly-once WebSocket delivery is not promised.

## 10. Membership changes and visibility boundaries

Membership changes race with message creation and asynchronous delivery. The
system therefore records a version or sequence boundary for membership changes.
For example, `joined_sequence` and `left_sequence` can define which messages a
member may see.

The owning conversation service decides visibility. A router or Gateway must
not infer access merely because it has a stale subscription. Control events
should include a monotonic membership version so Gateways can reject stale
updates. When correctness cannot be established, the Gateway drops the
real-time event and lets the client recover through the authorized
synchronization API.

## 11. Failure handling

| Failure | Required behavior |
| --- | --- |
| API crashes before commit | No message is acknowledged; the client may retry. |
| API crashes after commit but before its response | The same idempotency key returns the canonical message. |
| Outbox publication fails | The publisher retries and exposes outbox lag. |
| An event is published or consumed twice | Consumers deduplicate by `event_id`; clients deduplicate by `message_id`. |
| Delivery router crashes | Another consumer resumes from the durable event log. |
| Gateway crashes | Clients reconnect to another Gateway and synchronize. |
| Session registry contains stale data | TTL expiry removes the abandoned session. |
| A connection queue is full | The Gateway disconnects the slow client; the client later synchronizes. |
| Broker is temporarily unavailable | The message and outbox event remain durable and publication resumes later. |
| A membership change races with a message | Sequence or membership-version visibility rules decide access. |
| A client observes a gap | The client requests the missing range from the synchronization API. |

## 12. Horizontal scaling and partitioning

Each component scales independently:

- **Message API:** stateless instances behind a load balancer.
- **Outbox publisher:** workers claim rows safely or consume database CDC.
- **Durable event log:** partitions are keyed by `conversation_id`.
- **Delivery router:** a consumer group distributes conversation partitions
  across router instances.
- **Gateway:** instances scale based on active connections, memory, outbound
  bandwidth, queue depth, and event rate rather than CPU alone.
- **Session registry:** Redis Cluster or an equivalent partitioned ephemeral
  store shards by user or session key.
- **Message store:** indexes and partitions by `conversation_id`; database
  sharding can be introduced when a single cluster is no longer sufficient.

The durable input event must not be partitioned by recipient `user_id`, because
doing so would split one conversation's ordering across partitions. After
processing, real-time commands are routed by `gateway_instance_id` or by the
conversation subject.

Hot conversations require special monitoring. If one conversation exceeds a
single partition's sustainable throughput, product-level batching, event
coalescing, dedicated partitions, or an explicit relaxation of strict
per-conversation ordering is required. Adding ordinary consumers alone cannot
parallelize a single strictly ordered conversation.

## 13. Observability and capacity testing

At minimum, expose:

- message creation rate and transaction latency;
- outbox row count, oldest pending age, publication latency, and failures;
- durable broker producer errors, consumer lag, and partition skew;
- routing latency and recipients or Gateways per event;
- targeted versus conversation-subscription fan-out counts;
- session registry latency, errors, and stale-entry rate;
- active Gateway subscriptions and local fan-out size;
- per-connection outbound queue depth and slow-consumer disconnects;
- WebSocket events and bytes sent; and
- client gap detection, reconnect, and synchronization rates.

Logs and traces may contain `event_id`, `message_id`, `conversation_id`, safe
user IDs, Gateway instance ID, sequence, counts, latency, and trace ID. They
must not contain bearer tokens or private message bodies.

Capacity tests must include:

- a 10,000-member group with different online ratios;
- burst delivery across many Gateway instances;
- one very hot conversation and many ordinary conversations;
- multi-device users;
- slow consumers and bounded-queue exhaustion;
- Gateway crashes and reconnect storms;
- broker and session-registry degradation; and
- outbox backlog recovery without uncontrolled duplicate delivery.

## 14. Recommended implementation phases

### Phase 1: reliable delivery foundation

1. Store the message, conversation sequence, and outbox event transactionally.
2. Add one durable outbox publisher and a partitioned message event log.
3. Implement the distributed session registry.
4. Resolve sessions in batches and aggregate delivery by Gateway.
5. Add bounded per-connection Gateway queues and a single writer per socket.
6. Implement client deduplication, gap detection, and sequence-based catch-up.

### Phase 2: large-group fan-out

1. Add Gateway `conversation_id -> local connections` indexes.
2. Add dynamic subjects for large or hot conversations.
3. Publish membership control events with monotonic versions.
4. Introduce the hybrid targeted/conversation fan-out policy.
5. Load-test large groups and tune the selection thresholds.

### Phase 3: production optimization

1. Add region-aware routing and Gateway subjects.
2. Isolate or throttle hot conversations.
3. Add a separate push-notification pipeline.
4. Automate fan-out strategy selection from measured load.
5. Add dashboards, alerts, failure injection, and backlog-recovery tests.

## 15. Recommended initial technology shape

A practical initial deployment is:

```text
PostgreSQL or MySQL
    + transactional outbox
    + Kafka or JetStream durable message.created log
    + horizontally scaled delivery routers
    + Redis session registry
    + NATS real-time Gateway subjects
    + Gateway-local conversation fan-out
    + HTTP sequence-based synchronization
```

The exact products may change without changing the architectural contract. The
essential rule is that a group message is stored once, routed only to relevant
Gateway instances, expanded to online connections at the edge, and recovered
by offline clients through durable sequence-based synchronization.
