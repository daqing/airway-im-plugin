import Foundation

// Data model types mirroring deps/im/docs/api/openapi.md. Everything is
// decoded with JSONDecoder's convertFromSnakeCase, so a wire field such as
// `avatar_url` surfaces as `avatarUrl`.

/// How a conversation id routes in the UI.
public enum ConversationKind: String, Sendable, Codable {
    case direct
    case group
}

/// An open string enum: the two known roles ship as constants, unknown
/// values still decode instead of failing.
public struct MemberRole: Hashable, Sendable, Codable, RawRepresentable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let owner = MemberRole(rawValue: "owner")
    public static let admin = MemberRole(rawValue: "admin")
    public static let member = MemberRole(rawValue: "member")
}

/// How to render message `content`. `text/markdown` is the send default.
public struct ContentType: Hashable, Sendable, Codable, RawRepresentable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let markdown = ContentType(rawValue: "text/markdown")
    public static let plain = ContentType(rawValue: "text/plain")
}

public struct User: Sendable, Codable, Equatable {
    public let uuid: String
    public let username: String
    public let nickname: String?
    public let avatarUrl: String?
    public let email: String?
    public let lastSeenAt: String?
    public let createdAt: String
    public let updatedAt: String
}

public struct Sender: Sendable, Codable, Equatable {
    public let uuid: String
    public let username: String
    public let nickname: String?
    public let avatarUrl: String?
}

public struct ConversationSummary: Sendable, Codable, Equatable {
    public let id: String
    public let kind: ConversationKind
    public let title: String?
    public let avatarUrl: String?
    public let createdBy: String
    public let createdAt: String
    public let updatedAt: String
}

public struct ConversationMember: Sendable, Codable, Equatable {
    public let uuid: String
    public let username: String
    public let nickname: String?
    public let avatarUrl: String?
    public let role: MemberRole
}

/// Conversation kind plus members with roles, as returned by the details
/// endpoint. `conversationUuid` echoes the requested conversation id.
public struct ConversationDetails: Sendable, Codable, Equatable {
    public let conversationUuid: String
    public let type: ConversationKind
    public let title: String?
    public let members: [ConversationMember]
}

/// A message as returned by sends, history, and realtime events. Sort by
/// `sequence`, never by `createdAt`; dedupe by `id`.
public struct ChatMessage: Sendable, Codable, Equatable, Identifiable {
    /// Server-generated, globally unique ULID; the stable dedupe key.
    public let id: String
    /// Conversation the message belongs to; route messages by it.
    public let conversationId: String
    /// Author profile (current, not a send-time snapshot).
    public let sender: Sender
    /// Body, 1-32768 UTF-8 bytes, CRLF normalized to LF; a moderated
    /// message reads back as the literal `***`.
    public let content: String
    public let contentType: ContentType
    /// RFC 3339 UTC server commit timestamp.
    public let createdAt: String
    /// Position within the conversation, ≥ 1; `(conversationId, sequence)`
    /// is the total order.
    public let sequence: Int
}

public struct UploadResult: Sendable, Codable, Equatable {
    public let key: String
    public let url: String
    public let size: Int
}

/// Upload payload: a local file URL or in-memory data with a filename.
public enum FileInput: Sendable {
    case fileURL(URL)
    case data(Data, filename: String)
}

/// Gateway event frames (deps/im/docs/api/openapi.md, "WebSocket Gateway
/// companion contract"). Delivery is at-least-once; dedupe by `eventId`
/// (the SDK's gateway client already does).
public struct GatewayEvent: Sendable, Codable, Equatable {
    public let eventId: String
    public let event: String
    public let messageId: String?
    public let conversationId: String
    public let sequence: Int?
    public let addedUserUuids: [String]?
    public let removedUserUuid: String?
    public let targets: Targets?

    public struct Targets: Sendable, Codable, Equatable {
        public let userUuids: [String]
    }
}

/// Well-known gateway event names (the wire format is open; unknown
/// strings may appear and are surfaced via `onEvent`).
public enum GatewayEventType {
    public static let messageCreated = "message.created"
    public static let messageModerated = "message.moderated"
    public static let memberAdded = "conversation.member_added"
    public static let memberRemoved = "conversation.member_removed"
}

public enum ConnectionStatus: String, Sendable {
    case connecting
    case authenticating
    case online
    case reconnecting
    case offline
    case closed
}

/// Payload of the members-added notification.
public struct MembersAddedInfo: Sendable, Equatable {
    public let conversationId: String
    public let addedUserUUIDs: [String]
    public let event: GatewayEvent
}

/// Payload of the members-removed notification (a kicked user is notified
/// too).
public struct MembersRemovedInfo: Sendable, Equatable {
    public let conversationId: String
    public let removedUserUUID: String
    public let event: GatewayEvent
}
