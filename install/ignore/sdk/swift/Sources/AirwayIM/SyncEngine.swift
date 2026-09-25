import Foundation

/// Sequence-based synchronization engine.
///
/// The backend guarantees per-conversation monotonic `sequence` values and
/// at-least-once event delivery, so clients deduplicate by message_id /
/// event_id, order by `(conversation_id, sequence)`, and heal gaps by
/// fetching `after_sequence` (see deps/im/docs/api/openapi.md, "Client
/// requirements").
///
/// This engine owns the per-conversation last-seen sequence. All fetches
/// for one conversation run serialized — `fetchFrom` and realtime-driven
/// drains can never interleave, so each message is emitted exactly once,
/// in order. Conversations become tracked through `track`/`fetchFrom`;
/// events for untracked conversations are left to the application
/// (surfaced via the raw event handler).
actor SyncEngine {
    struct Handlers: Sendable {
        let onMessages: @Sendable ([ChatMessage]) -> Void
        let onMessageUpdated: @Sendable (ChatMessage) -> Void
    }

    private let http: RESTClient
    private let handlers: Handlers
    private let store: (any SequenceStore)?
    private let storageKey: String

    private var lastSeq: [String: Int]
    private var queueTails: [String: Task<Void, Never>] = [:]

    private static let pageLimit = 200
    /// Upper bound of pages fetched per drain cycle (10 × 200 messages).
    private static let maxSyncPages = 10

    init(
        http: RESTClient,
        handlers: Handlers,
        store: (any SequenceStore)?,
        storageKey: String = "airway-im.sequences"
    ) {
        self.http = http
        self.handlers = handlers
        self.store = store
        self.storageKey = storageKey
        self.lastSeq = Self.loadSequences(from: store, key: storageKey)
    }

    // ---- Sequence bookkeeping ----

    /// Mark a conversation as tracked, raising its last-seen sequence.
    func track(_ conversationId: String, _ sequence: Int) {
        let current = lastSeq[conversationId]
        if current == nil {
            lastSeq[conversationId] = max(0, sequence)
            persist()
            return
        }
        if sequence > current! {
            lastSeq[conversationId] = sequence
            persist()
        }
    }

    func isTracked(_ conversationId: String) -> Bool {
        lastSeq[conversationId] != nil
    }

    func lastSequence(_ conversationId: String) -> Int {
        lastSeq[conversationId] ?? 0
    }

    func forget(_ conversationId: String) {
        lastSeq[conversationId] = nil
        queueTails[conversationId] = nil
        persist()
    }

    /// Restores the persisted cursors; a corrupted cache starts clean
    /// rather than fail.
    private nonisolated static func loadSequences(
        from store: (any SequenceStore)?, key: String
    ) -> [String: Int] {
        guard let store, let raw = store.get(key) else { return [:] }
        guard
            let object = try? JSONSerialization.jsonObject(with: Data(raw.utf8)),
            let parsed = object as? [String: Any]
        else { return [:] }
        var result: [String: Int] = [:]
        for (id, value) in parsed {
            if let sequence = value as? Int, sequence > 0 {
                result[id] = sequence
            }
        }
        return result
    }

    private func persist() {
        guard let store else { return }
        guard let data = try? JSONSerialization.data(withJSONObject: lastSeq) else { return }
        store.set(storageKey, value: String(decoding: data, as: UTF8.self))
    }

    // ---- Serialized fetching ----

    /// Serialized per-conversation fetch; returns the messages it emitted.
    func fetchFrom(
        _ conversationId: String,
        fromSequence: Int? = nil,
        limit: Int? = nil,
        maxPages: Int? = nil
    ) async throws -> [ChatMessage] {
        let task = enqueue(conversationId) {
            try await self.runFetch(conversationId, fromSequence: fromSequence, limit: limit, maxPages: maxPages)
        }
        return try await task.value
    }

    /// Serialized reload of one masked (moderated) message.
    func reloadMessage(_ conversationId: String, event: GatewayEvent) async throws -> ChatMessage? {
        let task = enqueue(conversationId) { () async throws -> ChatMessage? in
            guard let sequence = event.sequence else { return nil }
            // The sequence may already be cached; reload starting at
            // sequence - 1 and replace the local message with the masked
            // API response.
            let page = try await self.http.listMessages(
                conversationId, afterSequence: sequence - 1, limit: 10)
            return page.first { $0.id == event.messageId }
        }
        return try await task.value
    }

    /// Resynchronize every tracked conversation after a (re)connect.
    func resyncAll() async {
        let ids = Array(lastSeq.keys)
        await withTaskGroup(of: Void.self) { group in
            for id in ids {
                group.addTask {
                    _ = try? await self.fetchFrom(id)
                }
            }
        }
    }

    /// Serialized task chaining: each operation for a conversation runs
    /// after the previous one finished, whether it resolved or failed, so a
    /// single network error cannot wedge the conversation's queue.
    private func enqueue<Value: Sendable>(
        _ conversationId: String,
        _ operation: @escaping @Sendable () async throws -> Value
    ) -> Task<Value, Error> {
        let previous = queueTails[conversationId]
        let task = Task<Value, Error> {
            if let previous { _ = try? await previous.value }
            return try await operation()
        }
        queueTails[conversationId] = Task<Void, Never> {
            _ = try? await task.value
        }
        return task
    }

    private func runFetch(
        _ conversationId: String,
        fromSequence: Int?,
        limit: Int?,
        maxPages: Int?
    ) async throws -> [ChatMessage] {
        let pageSize = limit ?? Self.pageLimit
        var from = fromSequence ?? lastSequence(conversationId)
        let pages = maxPages ?? Self.maxSyncPages
        var all: [ChatMessage] = []

        for _ in 0..<pages {
            let messages = try await http.listMessages(
                conversationId, afterSequence: from, limit: pageSize)
            if messages.isEmpty { break }
            all.append(contentsOf: messages)
            handlers.onMessages(messages)
            from = messages[messages.count - 1].sequence
            if messages.count < pageSize { break }
        }
        // Track even when the page was empty: the caller has seen
        // everything up to `from` (0 for fresh conversations), so later
        // events can sync.
        track(conversationId, from)
        return all
    }

    // ---- Event-driven sync ----

    /// Handle a gateway message.created event (serialized per conversation).
    func handleMessageEvent(_ event: GatewayEvent) async {
        let conversationId = event.conversationId
        guard isTracked(conversationId) else { return } // untracked: app decides
        if let target = event.sequence, target <= lastSequence(conversationId) {
            return // duplicate or already-applied (e.g. our own send)
        }
        // The next event for this conversation retries; nothing is lost
        // because the sequence tracker still points at the last applied
        // message.
        _ = try? await fetchFrom(conversationId)
    }

    /// Handle a gateway message.moderated event by reloading the masked
    /// message.
    func handleMessageModeratedEvent(_ event: GatewayEvent) async {
        guard isTracked(event.conversationId) else { return }
        if let updated = try? await reloadMessage(event.conversationId, event: event) {
            handlers.onMessageUpdated(updated)
        }
    }
}
