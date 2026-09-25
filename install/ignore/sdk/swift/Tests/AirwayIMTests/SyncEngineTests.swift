import Testing
import Foundation
@testable import AirwayIM

@Suite("SyncEngine")
struct SyncEngineTests {
    private static func message(_ sequence: Int, conversation: String = "01AB") -> [String: Any] {
        [
            "id": "01J\(String(sequence))", "conversation_id": conversation,
            "sender": ["uuid": "u1", "username": "alice", "nickname": nil, "avatar_url": nil],
            "content": "m\(sequence)", "content_type": "text/markdown",
            "created_at": "t", "sequence": sequence,
        ]
    }

    @Test("fetchFrom pages in order and tracks the cursor")
    func fetchPagination() async throws {
        let queries = Capture<String>()
        let emitted = Capture<[Int]>()
        let server = try StubHTTPServer(handler: { request in
            queries.append(request.query["after_sequence"] ?? "-")
            switch request.query["after_sequence"] {
            case "0": return StubResponse(json: envelope([Self.message(1), Self.message(2)]))
            case "2": return StubResponse(json: envelope([Self.message(3), Self.message(4)]))
            default: return StubResponse(json: envelope([Int]()))
            }
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(baseURL: server.baseURL, credential: "c")
        let emittedBox = LockedBox(emitted)
        let sync = SyncEngine(http: client, handlers: .init(
            onMessages: { emittedBox.current.append($0.map(\.sequence)) },
            onMessageUpdated: { _ in }), store: nil)

        let all = try await sync.fetchFrom("01AB", fromSequence: 0, limit: 2)
        #expect(all.map(\.sequence) == [1, 2, 3, 4])
        #expect(emitted.all == [[1, 2], [3, 4]])
        #expect(queries.all == ["0", "2", "4"])
        #expect(await sync.lastSequence("01AB") == 4)
        #expect(await sync.isTracked("01AB"))
    }

    @Test("handleMessageEvent heals gaps for tracked conversations only")
    func gapHealing() async throws {
        let queries = Capture<String>()
        let emitted = Capture<[Int]>()
        let server = try StubHTTPServer(handler: { _ in
            queries.append("fetch")
            return StubResponse(json: envelope([Self.message(6), Self.message(7)]))
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(baseURL: server.baseURL, credential: "c")
        let emittedBox = LockedBox(emitted)
        let sync = SyncEngine(http: client, handlers: .init(
            onMessages: { emittedBox.current.append($0.map(\.sequence)) },
            onMessageUpdated: { _ in }), store: nil)

        // Untracked: ignored.
        await sync.handleMessageEvent(GatewayEvent(
            eventId: "e1", event: GatewayEventType.messageCreated, messageId: nil,
            conversationId: "01AB", sequence: 5, addedUserUuids: nil,
            removedUserUuid: nil, targets: nil))
        try await Task.sleep(for: .milliseconds(50))
        #expect(queries.count == 0)

        await sync.track("01AB", 4)

        // Already applied (sequence ≤ cursor): ignored.
        await sync.handleMessageEvent(GatewayEvent(
            eventId: "e2", event: GatewayEventType.messageCreated, messageId: nil,
            conversationId: "01AB", sequence: 4, addedUserUuids: nil,
            removedUserUuid: nil, targets: nil))
        try await Task.sleep(for: .milliseconds(50))
        #expect(queries.count == 0)

        // Gap: fetched and emitted in order.
        await sync.handleMessageEvent(GatewayEvent(
            eventId: "e3", event: GatewayEventType.messageCreated, messageId: nil,
            conversationId: "01AB", sequence: 7, addedUserUuids: nil,
            removedUserUuid: nil, targets: nil))
        try await waitFor("gap fetched") { queries.count == 1 }
        try await waitFor("messages emitted") { emitted.count == 1 }
        #expect(emitted.all[0] == [6, 7])
        #expect(await sync.lastSequence("01AB") == 7)
    }

    @Test("moderated events reload the masked message")
    func moderatedReload() async throws {
        let updated = Capture<ChatMessage>()
        let queries = Capture<String>()
        let server = try StubHTTPServer(handler: { request in
            queries.append(request.query["after_sequence"] ?? "-")
            return StubResponse(json: envelope([
                Self.message(5).merging(["content": "***"], uniquingKeysWith: { _, new in new }),
            ]))
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(baseURL: server.baseURL, credential: "c")
        let updatedBox = LockedBox(updated)
        let sync = SyncEngine(http: client, handlers: .init(
            onMessages: { _ in },
            onMessageUpdated: { updatedBox.current.append($0) }), store: nil)
        await sync.track("01AB", 5)

        await sync.handleMessageModeratedEvent(GatewayEvent(
            eventId: "m1", event: GatewayEventType.messageModerated, messageId: "01J5",
            conversationId: "01AB", sequence: 5, addedUserUuids: nil,
            removedUserUuid: nil, targets: nil))
        try await waitFor("updated emitted") { updated.count == 1 }
        #expect(queries.all == ["4"])
        #expect(updated.all[0].content == "***")
        #expect(updated.all[0].id == "01J5")
    }

    @Test("cursors persist into the store and reload on init")
    func persistence() async throws {
        let server = try StubHTTPServer(handler: { _ in
            StubResponse(json: envelope([Self.message(1)]))
        })
        server.start()
        defer { server.stop() }

        let store = InMemorySequenceStore()
        let client = RESTClient(baseURL: server.baseURL, credential: "c")
        let sync = SyncEngine(http: client, handlers: .init(
            onMessages: { _ in }, onMessageUpdated: { _ in }), store: store)
        _ = try await sync.fetchFrom("01AB", fromSequence: 0)
        #expect(await sync.lastSequence("01AB") == 1)

        let raw = store.get("airway-im.sequences")
        #expect(raw?.contains("01AB") == true)

        // A fresh engine instance restores the cursor.
        let revived = SyncEngine(http: client, handlers: .init(
            onMessages: { _ in }, onMessageUpdated: { _ in }), store: store)
        let restored = await revived.lastSequence("01AB")
        #expect(restored == 1)
    }
}
