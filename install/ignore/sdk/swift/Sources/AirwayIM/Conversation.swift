import Foundation

/// Conversation handles: one object per conversation with its own event
/// subscriptions, so a chat window subscribes directly instead of
/// filtering the global stream by conversation id. The kind lives on the
/// object (`DirectConversation` / `GroupConversation`), and the first
/// `onMessage` listener starts tracking the conversation automatically.
/// Handle objects are cached per conversation id inside the session:
/// opening the same conversation twice yields the same object.
public class Conversation: @unchecked Sendable {
    /// The owning session; internal, not part of the public surface.
    weak var im: AirwayIM?

    public let id: String
    public let kind: ConversationKind

    let messageListeners = ListenerList<ChatMessage>()
    let messageUpdatedListeners = ListenerList<ChatMessage>()
    let membersAddedListeners = ListenerList<MembersAddedInfo>()
    let membersRemovedListeners = ListenerList<MembersRemovedInfo>()

    init(id: String, kind: ConversationKind, im: AirwayIM) {
        self.id = id
        self.kind = kind
        self.im = im
    }

    // ---- Events ----

    /// Subscribe to this conversation's messages: ordered, deduplicated,
    /// gap-filled. The first listener starts tracking automatically — it
    /// fetches everything since the last persisted cursor (`history`
    /// semantics) and keeps the conversation in sync from then on.
    @discardableResult
    public func onMessage(_ handler: @escaping @Sendable (ChatMessage) -> Void) -> Subscription {
        let subscription = messageListeners.add(handler)
        Task { await im?.ensureTracked(id) }
        return subscription
    }

    /// A previously seen message in this conversation was masked by
    /// moderation; `content` is already `***` — re-render by replacing.
    @discardableResult
    public func onMessageUpdated(_ handler: @escaping @Sendable (ChatMessage) -> Void) -> Subscription {
        messageUpdatedListeners.add(handler)
    }

    /// Group conversations only: members were added (includes the added
    /// users themselves).
    @discardableResult
    public func onMembersAdded(_ handler: @escaping @Sendable (MembersAddedInfo) -> Void) -> Subscription {
        membersAddedListeners.add(handler)
    }

    /// Group conversations only: a member was removed (a kicked user is
    /// notified too).
    @discardableResult
    public func onMembersRemoved(_ handler: @escaping @Sendable (MembersRemovedInfo) -> Void) -> Subscription {
        membersRemovedListeners.add(handler)
    }

    // ---- Operations ----

    /// Initial load: fetch messages since the last persisted cursor (or
    /// `fromSequence`) and emit them here and on the global stream. Mostly
    /// redundant after `onMessage` — that already auto-tracks — but useful
    /// to await the backlog before rendering.
    public func history(fromSequence: Int? = nil, limit: Int? = nil) async throws -> [ChatMessage] {
        guard let im else { throw releasedError }
        return try await im.history(id, fromSequence: fromSequence, limit: limit)
    }

    /// Raw ordered message page after a sequence (no state changes, no
    /// event emission).
    public func listMessages(afterSequence: Int? = nil, limit: Int? = nil) async throws -> [ChatMessage] {
        guard let im else { throw releasedError }
        return try await im.rest.listMessages(id, afterSequence: afterSequence, limit: limit)
    }

    /// Send a message to this conversation (same idempotency/retry
    /// semantics as `sendGroupMessage`).
    public func send(
        _ content: String,
        contentType: ContentType = .markdown,
        idempotencyKey: String? = nil,
        retries: Int = 1
    ) async throws -> ChatMessage {
        guard let im else { throw releasedError }
        return try await im.rest.sendMessage(
            id, content,
            contentType: contentType,
            idempotencyKey: idempotencyKey,
            retries: retries)
    }

    /// Conversation kind plus members with roles, fresh from the API.
    public func details() async throws -> ConversationDetails {
        guard let im else { throw releasedError }
        return try await im.rest.getConversation(id)
    }

    /// Last synced sequence in this conversation.
    public func lastSequence() async -> Int {
        guard let im else { return 0 }
        return await im.lastSequence(id)
    }

    /// Drop all sync state (e.g. after being kicked from the group).
    public func forget() async {
        await im?.forgetConversation(id)
    }

    // ---- Internal dispatch ----

    func emitLocalMessage(_ message: ChatMessage) { messageListeners.emit(message) }
    func emitLocalMessageUpdated(_ message: ChatMessage) { messageUpdatedListeners.emit(message) }
    func emitLocalMembersAdded(_ info: MembersAddedInfo) { membersAddedListeners.emit(info) }
    func emitLocalMembersRemoved(_ info: MembersRemovedInfo) { membersRemovedListeners.emit(info) }

    private var releasedError: IMError {
        IMError(code: -1, message: "the owning AirwayIM session has been released", status: 0)
    }
}

/// A direct (1:1) conversation; the peer of an incoming message is
/// `message.sender`.
public final class DirectConversation: Conversation, @unchecked Sendable {}

/// A group conversation; carries the group-only operations.
public final class GroupConversation: Conversation, @unchecked Sendable {
    /// Title from creation time (nil when created without one or when
    /// opened by id); `details()` returns the live value.
    public let title: String?

    init(id: String, title: String?, im: AirwayIM) {
        self.title = title
        super.init(id: id, kind: .group, im: im)
    }

    /// Add members by uuid (owner/admin; idempotent for active members).
    @discardableResult
    public func addMembers(_ memberUUIDs: [String]) async throws -> ConversationDetails {
        guard let im else { throw IMError(code: -1, message: "the owning AirwayIM session has been released", status: 0) }
        return try await im.addMembers(id, memberUUIDs: memberUUIDs)
    }

    /// Remove members by uuid (owner/admin; cannot remove self or the
    /// owner).
    @discardableResult
    public func removeMembers(_ userUUIDs: [String]) async throws -> ConversationDetails {
        guard let im else { throw IMError(code: -1, message: "the owning AirwayIM session has been released", status: 0) }
        return try await im.removeMembers(id, userUUIDs: userUUIDs)
    }
}
