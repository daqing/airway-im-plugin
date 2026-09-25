import Foundation

/// AirwayIM: the high-level entry point combining the REST client, the
/// WebSocket gateway, and the sequence-based sync engine behind one typed
/// event-emitting facade. This is what client apps should use.
///
///     let im = AirwayIM(
///         apiURL: "https://im.example.com",
///         wsURL: "wss://im.example.com",   // omit for REST-only use
///         credential: hostIssuedCredential)
///     try await im.connect()
///     let direct = try await im.createDirect(otherUserUUID)
///     direct.onMessage { message in print(message.sender.username, message.content) }
///     try await direct.send("hi")
///
/// The type is an actor: every method is async and safe to call from any
/// executor; event handlers are invoked on arbitrary executors and must be
/// `@Sendable`.
public actor AirwayIM {
    /// REST access — also usable directly for REST-only integrations.
    public let rest: RESTClient

    private let hub: EventHub
    private let sync: SyncEngine
    private let gateway: GatewaySocket?
    private let pumpBox = LockedBox<Task<Void, Never>?>(nil)

    /// - Parameters:
    ///   - apiURL: IM backend base URL, e.g. https://im.example.com (the
    ///     :1905 service).
    ///   - wsURL: Gateway WebSocket base URL, e.g. wss://im.example.com
    ///     (the :1910 service); the SDK connects to `<wsURL>/ws`. Omit for
    ///     REST-only use.
    ///   - credential: Host-signed credential issued by your own backend.
    ///   - getCredential: Called once when the credential is rejected
    ///     (HTTP 401/10001 or gateway auth failure) to fetch a fresh one
    ///     from your backend — e.g. via your login session. Keep it fast;
    ///     realtime resumes automatically with the returned credential.
    ///   - timeout: Per-request timeout in seconds (default 15).
    ///   - pingInterval: Gateway application-level ping interval in
    ///     seconds; 0 disables (default 25).
    ///   - persistSequences: Persist per-conversation last-seen sequences
    ///     via `sequenceStore` (default true; store defaults to
    ///     `UserDefaults`).
    ///   - sequenceStore: Where sequence cursors persist; pass
    ///     `InMemorySequenceStore()` (or `persistSequences: false`) to keep
    ///     cursors in memory.
    ///   - httpTransport: Custom REST transport; defaults to URLSession.
    ///   - socketFactory: Custom WebSocket transport factory; defaults to
    ///     URLSession sockets.
    ///   - autoConnect: Connect the gateway immediately (default false;
    ///     call `connect()` yourself).
    public init(
        apiURL: String,
        wsURL: String? = nil,
        credential: String,
        getCredential: CredentialProvider? = nil,
        timeout: TimeInterval = 15,
        pingInterval: TimeInterval = 25,
        persistSequences: Bool = true,
        sequenceStore: (any SequenceStore)? = nil,
        httpTransport: (any HTTPTransport)? = nil,
        socketFactory: (@Sendable (URL) -> any WebSocketTransport)? = nil,
        autoConnect: Bool = false
    ) {
        let hub = EventHub()
        self.hub = hub

        let rest = RESTClient(
            baseURL: apiURL,
            credential: credential,
            getCredential: getCredential,
            transport: httpTransport)
        self.rest = rest

        let store: (any SequenceStore)? =
            persistSequences ? (sequenceStore ?? UserDefaultsSequenceStore()) : nil
        let sync = SyncEngine(
            http: rest,
            handlers: SyncEngine.Handlers(
                onMessages: { hub.emitMessages($0) },
                onMessageUpdated: { hub.emitMessageUpdated($0) }),
            store: store)
        self.sync = sync

        if let wsURL {
            let factory: @Sendable (URL) -> any WebSocketTransport =
                socketFactory ?? { URLSessionWebSocketTransport(url: $0, timeout: timeout) }
            self.gateway = GatewaySocket(
                wsURL: wsURL,
                credential: credential,
                pingInterval: pingInterval,
                getCredential: getCredential,
                socketFactory: factory,
                callbacks: GatewayCallbacks(
                    onEvent: { hub.pushEvent($0) },
                    onStatus: { hub.setStatus($0) },
                    onReady: { hub.markReady() },
                    onError: { hub.emitError($0) }))
        } else {
            self.gateway = nil
        }

        // Serialized event pump: gateway frames are handled one at a time,
        // in arrival order.
        pumpBox.set(hub.pump { [weak self] event in
            await self?.handleEvent(event)
        })

        // Realtime resync after every successful (re)authentication.
        hub.readyListeners.add { [weak self] _ in
            Task { await self?.resync() }
        }

        if autoConnect {
            Task { await self.connect() }
        }
    }

    deinit {
        pumpBox.current?.cancel()
    }

    // ---- Events ----

    /// New messages, deduplicated and ordered; includes your own sends.
    @discardableResult
    public nonisolated func onMessage(_ handler: @escaping @Sendable (ChatMessage) -> Void) -> Subscription {
        hub.messageListeners.add(handler)
    }

    /// A previously seen message was masked by moderation (content `***`).
    @discardableResult
    public nonisolated func onMessageUpdated(_ handler: @escaping @Sendable (ChatMessage) -> Void) -> Subscription {
        hub.messageUpdatedListeners.add(handler)
    }

    /// Group members were added (including the added users themselves).
    @discardableResult
    public nonisolated func onMembersAdded(_ handler: @escaping @Sendable (MembersAddedInfo) -> Void) -> Subscription {
        hub.membersAddedListeners.add(handler)
    }

    /// A group member was removed (a kicked user is notified too).
    @discardableResult
    public nonisolated func onMembersRemoved(_ handler: @escaping @Sendable (MembersRemovedInfo) -> Void) -> Subscription {
        hub.membersRemovedListeners.add(handler)
    }

    /// Connection lifecycle changes.
    @discardableResult
    public nonisolated func onStatus(_ handler: @escaping @Sendable (ConnectionStatus) -> Void) -> Subscription {
        hub.statusListeners.add(handler)
    }

    /// Gateway auth failures, credential renewal failures, and listener
    /// wiring problems.
    @discardableResult
    public nonisolated func onError(_ handler: @escaping @Sendable (IMError) -> Void) -> Subscription {
        hub.errorListeners.add(handler)
    }

    /// Every raw gateway frame, including events for untracked
    /// conversations (use for unread badges and similar cases).
    @discardableResult
    public nonisolated func onEvent(_ handler: @escaping @Sendable (GatewayEvent) -> Void) -> Subscription {
        hub.rawEventListeners.add(handler)
    }

    /// Latest reported connection status (`.closed` before connecting).
    public nonisolated var connectionStatus: ConnectionStatus {
        hub.status
    }

    // ---- Realtime ----

    /// Open the gateway connection (no-op in REST-only mode).
    public func connect() async {
        guard let gateway else {
            hub.emitError(IMError(
                code: -1, message: "No wsURL configured; realtime is disabled", status: 0))
            return
        }
        await gateway.connect()
    }

    /// Close the gateway connection and stop reconnecting.
    public func disconnect() async {
        await gateway?.close()
    }

    public var isOnline: Bool {
        get async { await gateway?.isOnline ?? false }
    }

    /// Replace the credential everywhere (e.g. after your own re-login flow).
    public func setCredential(_ credential: String) async {
        await rest.setCredential(credential)
        await gateway?.setCredential(credential)
    }

    // ---- REST passthrough ----

    public func me() async throws -> User {
        try await rest.me()
    }

    /// List my active group conversations (direct ones are excluded by the
    /// backend).
    public func listGroups() async throws -> [ConversationSummary] {
        let conversations = try await rest.listGroups()
        for conversation in conversations {
            hub.registry.rememberKind(conversation.id, .group)
        }
        return conversations
    }

    /// Get-or-create the direct (1:1) conversation with one user, by uuid.
    public func createDirect(_ otherUserUUID: String) async throws -> DirectConversation {
        let conversation = try await rest.createDirect(otherUserUUID: otherUserUUID)
        hub.registry.rememberKind(conversation.id, .direct)
        return hub.registry.directHandle(conversation.id, im: self)
    }

    /// The existing direct conversation with one user, by uuid, as a
    /// handle — or nil when none exists yet (`createDirect` get-or-creates
    /// instead).
    public func getDirect(_ otherUserUUID: String) async throws -> DirectConversation? {
        guard let conversation = try await rest.getDirect(otherUserUUID: otherUserUUID) else {
            return nil
        }
        hub.registry.rememberKind(conversation.id, .direct)
        return hub.registry.directHandle(conversation.id, im: self)
    }

    /// Create a group conversation; the authenticated user becomes its
    /// owner.
    public func createGroup(title: String?, memberUUIDs: [String]) async throws -> GroupConversation {
        let conversation = try await rest.createGroup(title: title, memberUUIDs: memberUUIDs)
        hub.registry.rememberKind(conversation.id, .group)
        return hub.registry.groupHandle(conversation.id, title: title, im: self)
    }

    /// Open any conversation by id as a conversation object (e.g. one
    /// learned from a message event or `listGroups`). The kind is answered
    /// from the registry when this instance already saw the conversation,
    /// otherwise fetched via REST once; throws if the user cannot see the
    /// conversation.
    public func openConversation(_ conversationId: String) async throws -> Conversation {
        if let existing = hub.registry.conversation(conversationId) { return existing }
        var kind = hub.registry.kind(conversationId)
        if kind == nil {
            let details = try await rest.getConversation(conversationId)
            kind = details.type
            hub.registry.rememberKind(details.conversationUuid, details.type)
        }
        return hub.registry.handle(conversationId, kind: kind!, im: self)
    }

    /// Add members (by uuid) to a group (owner/admin; idempotent for
    /// already-active members).
    @discardableResult
    public func addMembers(
        _ conversationId: String,
        memberUUIDs: [String]
    ) async throws -> ConversationDetails {
        // Member management is group-only server-side.
        hub.registry.rememberKind(conversationId, .group)
        return try await rest.addMembers(conversationId, memberUUIDs: memberUUIDs)
    }

    /// Remove members (by uuid) from a group (owner/admin; cannot remove
    /// self or the owner).
    @discardableResult
    public func removeMembers(
        _ conversationId: String,
        userUUIDs: [String]
    ) async throws -> ConversationDetails {
        hub.registry.rememberKind(conversationId, .group)
        return try await rest.removeMembers(conversationId, userUUIDs: userUUIDs)
    }

    /// Initial load for a conversation: fetch messages after `fromSequence`
    /// (default: last persisted sequence, else 0), track the sequence, and
    /// emit each message via `onMessage` handlers. After this, the
    /// conversation is tracked and realtime events auto-heal gaps for it.
    public func history(
        _ conversationId: String,
        fromSequence: Int? = nil,
        limit: Int? = nil
    ) async throws -> [ChatMessage] {
        try await sync.fetchFrom(
            conversationId, fromSequence: fromSequence, limit: limit)
    }

    /// Raw ordered message page after a sequence (no state changes, no
    /// event emission). Most apps should use `history` + realtime events
    /// instead.
    public func listMessages(
        _ conversationId: String,
        afterSequence: Int? = nil,
        limit: Int? = nil
    ) async throws -> [ChatMessage] {
        try await rest.listMessages(
            conversationId, afterSequence: afterSequence, limit: limit)
    }

    /// Send a message to a group conversation by id. Returns the stored
    /// message; the local sequence tracker is updated so the sender's own
    /// message.created event does not trigger a redundant fetch. The
    /// message is emitted via `onMessage` only when it arrives back through
    /// realtime/sync (at-least-once) — use the return value for immediate
    /// UI feedback.
    @discardableResult
    public func sendGroupMessage(
        _ conversationId: String,
        _ content: String,
        contentType: ContentType = .markdown,
        idempotencyKey: String? = nil,
        retries: Int = 1
    ) async throws -> ChatMessage {
        hub.registry.rememberKind(conversationId, .group)
        let message = try await rest.sendMessage(
            conversationId, content,
            contentType: contentType,
            idempotencyKey: idempotencyKey,
            retries: retries)
        await sync.track(conversationId, message.sequence)
        return message
    }

    /// Send a direct message to one other user, identified by their uuid:
    /// get-or-create the direct conversation, then send. Same idempotency
    /// semantics as `sendGroupMessage`.
    @discardableResult
    public func sendDirectMessage(
        _ otherUserUUID: String,
        _ content: String,
        contentType: ContentType = .markdown,
        idempotencyKey: String? = nil,
        retries: Int = 1
    ) async throws -> ChatMessage {
        let message = try await rest.sendDirectMessage(
            otherUserUUID, content,
            contentType: contentType,
            idempotencyKey: idempotencyKey,
            retries: retries)
        await sync.track(message.conversationId, message.sequence)
        return message
    }

    /// Last synced sequence in a conversation.
    public func lastSequence(_ conversationId: String) async -> Int {
        await sync.lastSequence(conversationId)
    }

    /// Drop all sync state for a conversation (e.g. after being kicked).
    public func forgetConversation(_ conversationId: String) async {
        await sync.forget(conversationId)
        hub.registry.removeKind(conversationId)
    }

    // ---- Storage ----

    /// Upload a file; returns `{key, url, size}`.
    public func uploadFile(_ file: FileInput, dir: String? = nil) async throws -> UploadResult {
        try await rest.uploadFile(file, dir: dir)
    }

    /// Public URL for a storage key (for `<img src>`, downloads, …).
    public func storageURL(_ key: String) async -> URL {
        await rest.storageURL(key)
    }

    // ---- Internal routing ----

    private func handleEvent(_ event: GatewayEvent) {
        hub.rawEventListeners.emit(event)
        switch event.event {
        case GatewayEventType.messageCreated:
            Task { await sync.handleMessageEvent(event) }
        case GatewayEventType.messageModerated:
            Task { await sync.handleMessageModeratedEvent(event) }
        case GatewayEventType.memberAdded:
            hub.registry.rememberKind(event.conversationId, .group)
            if let added = event.addedUserUuids, !added.isEmpty {
                hub.emitMembersAdded(MembersAddedInfo(
                    conversationId: event.conversationId,
                    addedUserUUIDs: added,
                    event: event))
            }
        case GatewayEventType.memberRemoved:
            hub.registry.rememberKind(event.conversationId, .group)
            if let removed = event.removedUserUuid {
                hub.emitMembersRemoved(MembersRemovedInfo(
                    conversationId: event.conversationId,
                    removedUserUUID: removed,
                    event: event))
            }
        default:
            break
        }
    }

    private func resync() async {
        await sync.resyncAll()
    }

    /// Conversation objects: start tracking on first message listener.
    func ensureTracked(_ conversationId: String) {
        Task {
            guard !(await sync.isTracked(conversationId)) else { return }
            do {
                _ = try await history(conversationId)
            } catch let error as IMError {
                hub.emitError(error)
            } catch {
                hub.emitError(IMError(
                    code: -1, message: error.localizedDescription, status: 0))
            }
        }
    }
}
