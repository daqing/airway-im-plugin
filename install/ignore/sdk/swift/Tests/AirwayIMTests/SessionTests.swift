import Testing
import Foundation
@testable import AirwayIM

@Suite("AirwayIM session")
struct SessionTests {
    /// REST stub covering the endpoints the session tests need.
    private static func makeServer() throws -> StubHTTPServer {
        let servedHistory = LockedBox(false)
        return try StubHTTPServer(handler: { request in
            switch (request.method, request.path) {
            case ("POST", "/api/v1/conversations"):
                return StubResponse(json: envelope([
                    "id": "01DIRECT01", "kind": "direct", "title": nil, "avatar_url": nil,
                    "created_by": "u-me", "created_at": "t", "updated_at": "t",
                ]))
            case ("GET", "/api/v1/conversations/direct/user-b"):
                return StubResponse(json: envelope([
                    "id": "01DIRECT01", "kind": "direct", "title": nil, "avatar_url": nil,
                    "created_by": "u-me", "created_at": "t", "updated_at": "t",
                ]))
            case ("GET", "/api/v1/conversations/direct/absent"):
                return StubResponse(json: envelope(nil, code: 11001, message: "not found"))
                    .with(status: 404)
            case ("GET", "/api/v1/conversations/01GROUP01"):
                return StubResponse(json: envelope([
                    "conversation_uuid": "01GROUP01", "type": "group", "title": "Team", "members": [],
                ]))
            case ("GET", "/api/v1/conversations/01GROUP01/messages"):
                // First fetch returns the full backlog; afterwards an empty
                // page (everything already seen).
                if !servedHistory.current {
                    servedHistory.set(true)
                    return StubResponse(json: envelope([Self.message(sequence: 1), Self.message(sequence: 2)]))
                }
                if request.query["after_sequence"] == "2" {
                    return StubResponse(json: envelope([Self.message(sequence: 3)]))
                }
                return StubResponse(json: envelope([String]()))
            case ("POST", "/api/v1/messages"):
                let body = try request.jsonBody()
                var message = Self.message(sequence: (body["content"] as? String == "third") ? 3 : 99)
                message["conversation_id"] = body["conversation_id"]
                message["content"] = body["content"]
                return StubResponse(json: envelope(message))
            default:
                return StubResponse(json: envelope(nil, code: 10003, message: "unexpected \(request.method) \(request.path)"))
                    .with(status: 400)
            }
        })
    }

    private static func message(sequence: Int) -> [String: Any] {
        [
            "id": "01MSG\(sequence)", "conversation_id": "01GROUP01",
            "sender": ["uuid": "u-me", "username": "me", "nickname": nil, "avatar_url": nil],
            "content": "m\(sequence)", "content_type": "text/markdown",
            "created_at": "t", "sequence": sequence,
        ]
    }

    private final class Box: @unchecked Sendable {
        let handles = Capture<DirectConversation>()
        let fakes = Capture<FakeWebSocketTransport>()
        let messages = Capture<ChatMessage>()
        let rawEvents = Capture<GatewayEvent>()
        let errors = Capture<IMError>()
    }

    @Test("conversation handles are cached per conversation id")
    func handleIdentity() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)

        let first = try await im.createDirect("user-b")
        #expect(first.id == "01DIRECT01")
        #expect(first.kind == .direct)

        let second = try await im.createDirect("user-b")
        #expect(first === second)

        let resolved = try await im.getDirect("user-b")
        #expect(resolved === first)

        let absent = try await im.getDirect("absent")
        #expect(absent == nil)

        let group = try await im.openConversation("01GROUP01")
        #expect(group is GroupConversation)
        // The title is creation-time only; opening by id starts without one
        // and `details()` returns the live value.
        #expect((group as? GroupConversation)?.title == nil)
        let details = try await group.details()
        #expect(details.title == "Team")
    }

    @Test("history() emits through onMessage, and gateway events sync the gaps")
    func realtimeFlow() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        let box = Box()
        let im = AirwayIM(
            apiURL: server.baseURL,
            wsURL: "ws://unused",
            credential: "c",
            persistSequences: false,
            socketFactory: { [box] _ in
                let fake = FakeWebSocketTransport.make()
                fake.onSend = { fake, text in
                    if text.contains("\"auth\"") {
                        Task { await fake.serverEnqueue(#"{"code":0,"data":"OK"}"#) }
                    }
                }
                box.fakes.append(fake)
                return fake
            })
        _ = im.onMessage { [box] in box.messages.append($0) }
        _ = im.onEvent { [box] in box.rawEvents.append($0) }

        await im.connect()
        let fake = try await waitForValue("gateway transport") { box.fakes.all.first }
        try await waitFor("online") { await im.isOnline }

        let conversation = try await im.openConversation("01GROUP01")
        #expect(conversation.kind == .group)
        let backlog = try await conversation.history()
        #expect(backlog.map(\.sequence) == [1, 2])
        try await waitFor("backlog emitted") { box.messages.count == 2 }

        // Realtime event with a gap to heal: sequence 3 → fetch after 2.
        await fake.serverEnqueue(eventFrame(
            eventId: "e3", conversationId: "01GROUP01", sequence: 3, messageId: "01MSG3"))
        try await waitFor("gap-filled message") { box.messages.count == 3 }
        #expect(box.messages.all[2].content == "m3")

        // Duplicate event (already applied): no re-emission.
        await fake.serverEnqueue(eventFrame(
            eventId: "e3-dup", conversationId: "01GROUP01", sequence: 3, messageId: "01MSG3"))
        try await Task.sleep(for: .milliseconds(150))
        #expect(box.messages.count == 3)

        await im.disconnect()
        try await waitFor("disconnected") { im.connectionStatus == .closed }
    }

    @Test("host notifications arrive with their payload, IM events do not")
    func hostNotifications() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        final class HostBox: @unchecked Sendable {
            let fakes = Capture<FakeWebSocketTransport>()
            let notifications = Capture<HostNotification>()
        }
        let box = HostBox()
        let im = AirwayIM(
            apiURL: server.baseURL,
            wsURL: "ws://unused",
            credential: "c",
            persistSequences: false,
            socketFactory: { [box] _ in
                let fake = FakeWebSocketTransport.make()
                fake.onSend = { fake, text in
                    if text.contains("\"auth\"") {
                        Task { await fake.serverEnqueue(#"{"code":0,"data":"OK"}"#) }
                    }
                }
                box.fakes.append(fake)
                return fake
            })
        _ = im.onHostNotification { [box] in box.notifications.append($0) }

        await im.connect()
        let fake = try await waitForValue("gateway transport") { box.fakes.all.first }
        try await waitFor("online") { await im.isOnline }

        await fake.serverEnqueue(#"{"event_id":"01HOST1","event":"host.friend_request","conversation_id":"","data":{"from_uuid":"u-a"},"targets":{"user_uuids":["u-me"]}}"#)
        try await waitFor("host notification") { !box.notifications.all.isEmpty }
        let notification = box.notifications.all[0]
        #expect(notification.event == "host.friend_request")
        #expect(notification.data?["from_uuid"]?.stringValue == "u-a")

        // IM domain events must not surface as host notifications.
        await fake.serverEnqueue(eventFrame(
            eventId: "e-im", conversationId: "01GROUP01", sequence: 1, messageId: "01MSG1"))
        try await Task.sleep(for: .milliseconds(150))
        #expect(box.notifications.all.count == 1)

        await im.disconnect()
        try await waitFor("disconnected") { im.connectionStatus == .closed }
    }

    @Test("a conversation's first onMessage listener starts tracking automatically")
    func autoTrack() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        let box = Box()
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        _ = im.onError { [box] in box.errors.append($0) }

        let conversation = try await im.openConversation("01GROUP01")
        _ = conversation.onMessage { [box] in box.messages.append($0) }
        try await waitFor("auto-tracked backlog") { box.messages.count == 2 }

        let cursor = await conversation.lastSequence()
        #expect(cursor == 2)
        #expect(box.errors.all.isEmpty)
    }

    @Test("sendDirectMessage get-or-creates the conversation, then sends")
    func sendDirectMessage() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        let message = try await im.sendDirectMessage("user-b", "third")
        #expect(message.content == "third")
        #expect(message.sequence == 3)

        let cursor = await im.lastSequence(message.conversationId)
        #expect(cursor == 3)
    }

    @Test("sendGroupMessage tracks the sender's own sequence; conversation.send uses it")
    func sendGroupMessage() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        let group = try await im.openConversation("01GROUP01")
        #expect(group is GroupConversation)

        let sent = try await im.sendGroupMessage("01GROUP01", "third")
        #expect(sent.sequence == 3)
        let cursor = await im.lastSequence("01GROUP01")
        #expect(cursor == 3)

        let viaHandle = try await group.send("via handle")
        #expect(viaHandle.content == "via handle")
    }

    @Test("REST-only sessions report an error from connect() instead of crashing")
    func restOnlyConnect() async throws {
        let server = try Self.makeServer()
        server.start()
        defer { server.stop() }

        let box = Box()
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        _ = im.onError { [box] in box.errors.append($0) }

        await im.connect()
        try await waitFor("error emitted") { !box.errors.all.isEmpty }
        #expect(box.errors.all[0].message.contains("No wsURL"))

        let online = await im.isOnline
        #expect(!online)
    }
}
