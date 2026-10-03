import Foundation

/// Thread-safe hub shared by the session actor, the sync engine, and the
/// gateway: owns the listener lists, the connection-status box, the
/// conversation registry, and the serialized gateway-event pump feed.
final class EventHub: @unchecked Sendable {
    let registry = ConversationRegistry()

    let statusListeners = ListenerList<ConnectionStatus>()
    let errorListeners = ListenerList<IMError>()
    let rawEventListeners = ListenerList<GatewayEvent>()
    let messageListeners = ListenerList<ChatMessage>()
    let messageUpdatedListeners = ListenerList<ChatMessage>()
    let membersAddedListeners = ListenerList<MembersAddedInfo>()
    let membersRemovedListeners = ListenerList<MembersRemovedInfo>()
    let hostNotificationListeners = ListenerList<HostNotification>()
    let readyListeners = ListenerList<Void>()

    private let statusBox = LockedBox(ConnectionStatus.closed)
    private let eventStream: AsyncStream<GatewayEvent>
    private let eventContinuation: AsyncStream<GatewayEvent>.Continuation

    init() {
        (eventStream, eventContinuation) = AsyncStream.makeStream(
            of: GatewayEvent.self, bufferingPolicy: .unbounded)
    }

    var events: AsyncStream<GatewayEvent> { eventStream }

    /// Start the serialized event pump; gateway frames are handled one at
    /// a time, in arrival order.
    func pump(with handler: @escaping @Sendable (GatewayEvent) async -> Void) -> Task<Void, Never> {
        Task {
            for await event in eventStream {
                await handler(event)
            }
        }
    }

    var status: ConnectionStatus {
        statusBox.current
    }

    // ---- Gateway callbacks (called from the gateway actor) ----

    func pushEvent(_ event: GatewayEvent) {
        eventContinuation.yield(event)
    }

    func setStatus(_ status: ConnectionStatus) {
        statusBox.set(status)
        statusListeners.emit(status)
    }

    func emitError(_ error: IMError) {
        errorListeners.emit(error)
    }

    func markReady() {
        readyListeners.emit(())
    }

    // ---- Sync engine handlers (called from sync fetches) ----

    func emitMessages(_ messages: [ChatMessage]) {
        for message in messages {
            messageListeners.emit(message)
            registry.conversation(message.conversationId)?.emitLocalMessage(message)
        }
    }

    func emitMessageUpdated(_ message: ChatMessage) {
        messageUpdatedListeners.emit(message)
        registry.conversation(message.conversationId)?.emitLocalMessageUpdated(message)
    }

    func emitMembersAdded(_ info: MembersAddedInfo) {
        membersAddedListeners.emit(info)
        registry.conversation(info.conversationId)?.emitLocalMembersAdded(info)
    }

    func emitMembersRemoved(_ info: MembersRemovedInfo) {
        membersRemovedListeners.emit(info)
        registry.conversation(info.conversationId)?.emitLocalMembersRemoved(info)
    }

    func emitHostNotification(_ notification: HostNotification) {
        hostNotificationListeners.emit(notification)
    }
}

/// Conversation ids → handle objects and remembered kinds. Member
/// management is group-only server-side, so member events also mark their
/// conversation as a group in the kind registry.
final class ConversationRegistry: @unchecked Sendable {
    private let lock = NSLock()
    private var conversations: [String: Conversation] = [:]
    private var kinds: [String: ConversationKind] = [:]

    func conversation(_ id: String) -> Conversation? {
        lock.lock()
        defer { lock.unlock() }
        return conversations[id]
    }

    /// The same object (with its listeners) is returned by every
    /// open/create call for that conversation.
    func directHandle(_ id: String, im: AirwayIM) -> DirectConversation {
        lock.lock()
        defer { lock.unlock() }
        if let existing = conversations[id] as? DirectConversation { return existing }
        let conversation = DirectConversation(id: id, kind: .direct, im: im)
        conversations[id] = conversation
        return conversation
    }

    func groupHandle(_ id: String, title: String?, im: AirwayIM) -> GroupConversation {
        lock.lock()
        defer { lock.unlock() }
        if let existing = conversations[id] as? GroupConversation { return existing }
        let conversation = GroupConversation(id: id, title: title, im: im)
        conversations[id] = conversation
        return conversation
    }

    /// Open any conversation by id as a handle; the kind comes from the
    /// registry.
    func handle(_ id: String, kind: ConversationKind, im: AirwayIM) -> Conversation {
        lock.lock()
        defer { lock.unlock() }
        if let existing = conversations[id] { return existing }
        let conversation: Conversation
        switch kind {
        case .direct:
            conversation = DirectConversation(id: id, kind: .direct, im: im)
        case .group:
            conversation = GroupConversation(id: id, title: nil, im: im)
        }
        conversations[id] = conversation
        return conversation
    }

    func rememberKind(_ id: String, _ kind: ConversationKind) {
        lock.lock()
        kinds[id] = kind
        lock.unlock()
    }

    func kind(_ id: String) -> ConversationKind? {
        lock.lock()
        defer { lock.unlock() }
        return kinds[id]
    }

    /// Drop the remembered kind only (`forgetConversation` semantics — the
    /// handle object stays for whoever still holds it).
    func removeKind(_ id: String) {
        lock.lock()
        kinds[id] = nil
        lock.unlock()
    }
}
