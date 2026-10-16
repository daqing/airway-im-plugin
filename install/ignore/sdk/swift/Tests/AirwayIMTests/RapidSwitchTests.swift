import Testing
import Foundation
@testable import AirwayIM

/// Reproduces the KongChat UI bug report: rapidly clicking through three
/// group conversations leaves the last-clicked one without history. The app
/// layer drives `history(id, fromSequence:)` from SwiftUI `.task(id:)`, so
/// each switch cancels the previous call mid-flight, and the app ignores the
/// return value — messages only reach its state through `onMessage`.
@Suite("Rapid conversation switching")
struct RapidSwitchTests {
    private static func message(_ sequence: Int, conversation: String) -> [String: Any] {
        [
            "id": "01\(conversation)\(String(sequence))", "conversation_id": conversation,
            "sender": ["uuid": "u1", "username": "alice", "nickname": nil, "avatar_url": nil],
            "content": "m\(sequence)", "content_type": "text/markdown",
            "created_at": "t", "sequence": sequence,
        ]
    }

    private static func historyServer(
        conversations: [String], perConversation: Int, requests: Capture<String>
    ) throws -> StubHTTPServer {
        try StubHTTPServer(handler: { request in
            // path: /api/v1/conversations/<id>/messages
            let parts = request.path.split(separator: "/").map(String.init)
            guard let conversationIndex = parts.firstIndex(of: "conversations"),
                  parts.count > conversationIndex + 1
            else { return StubResponse(json: envelope([String: Any]())) }
            let id = parts[conversationIndex + 1]
            requests.append("\(id)?\(request.query["after_sequence"] ?? "-")")
            let page = (0..<perConversation).map { Self.message($0 + 1, conversation: id) }
            return StubResponse(json: envelope(page))
        })
    }

    @Test("cancelled history calls do not starve the last conversation (immediate cancel)")
    func immediateCancel() async throws {
        let requests = Capture<String>()
        let server = try Self.historyServer(
            conversations: ["convA", "convB", "convC"], perConversation: 2, requests: requests)
        server.start()
        defer { server.stop() }

        let received = Capture<String>()
        let receivedBox = LockedBox(received)
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        _ = im.onMessage { receivedBox.current.append("\($0.conversationId):\($0.sequence)") }

        // SwiftUI `.task(id:)` semantics: switching cancels the previous
        // task. The app's `open` then hits its catch and rolls back.
        let taskA = Task { try await im.history("convA", fromSequence: 0) }
        taskA.cancel()
        _ = try? await taskA.value
        let taskB = Task { try await im.history("convB", fromSequence: 0) }
        taskB.cancel()
        _ = try? await taskB.value

        let last = try await im.history("convC", fromSequence: 0)
        #expect(last.map(\.sequence) == [1, 2], "history return value for the last conversation")

        try await waitFor("onMessage delivered convC history") {
            received.all.contains("convC:1") && received.all.contains("convC:2")
        }
    }

    @Test("cancelled history calls do not starve the last conversation (in-flight cancel)")
    func inFlightCancel() async throws {
        let requests = Capture<String>()
        let server = try Self.historyServer(
            conversations: ["convA", "convB", "convC"], perConversation: 2, requests: requests)
        server.start()
        defer { server.stop() }

        let received = Capture<String>()
        let receivedBox = LockedBox(received)
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        _ = im.onMessage { receivedBox.current.append("\($0.conversationId):\($0.sequence)") }

        // Requests already on the wire when the switch cancels the caller.
        let taskA = Task { try await im.history("convA", fromSequence: 0) }
        try await waitFor("convA fetch enqueued") { requests.all.contains { $0.hasPrefix("convA") } }
        taskA.cancel()
        _ = try? await taskA.value
        let taskB = Task { try await im.history("convB", fromSequence: 0) }
        try await waitFor("convB fetch enqueued") { requests.all.contains { $0.hasPrefix("convB") } }
        taskB.cancel()
        _ = try? await taskB.value

        let last = try await im.history("convC", fromSequence: 0)
        #expect(last.map(\.sequence) == [1, 2], "history return value for the last conversation")

        try await waitFor("onMessage delivered convC history") {
            received.all.contains("convC:1") && received.all.contains("convC:2")
        }
    }

    @Test("A→B→A: reopening before the cancelled call finishes still delivers history")
    func reopenWhileCancelling() async throws {
        let requests = Capture<String>()
        let server = try Self.historyServer(
            conversations: ["convA", "convB"], perConversation: 2, requests: requests)
        server.start()
        defer { server.stop() }

        let received = Capture<String>()
        let receivedBox = LockedBox(received)
        let im = AirwayIM(apiURL: server.baseURL, credential: "c", persistSequences: false)
        _ = im.onMessage { receivedBox.current.append("\($0.conversationId):\($0.sequence)") }

        let taskA1 = Task { try await im.history("convA", fromSequence: 0) }
        let taskB = Task { try await im.history("convB", fromSequence: 0) }
        taskA1.cancel()
        taskB.cancel()
        // The app reopens A before the cancelled task's catch ran.
        let reopened = try await im.history("convA", fromSequence: 0)
        #expect(reopened.map(\.sequence) == [1, 2])

        try await waitFor("onMessage delivered convA history") {
            received.all.contains("convA:1") && received.all.contains("convA:2")
        }
    }

    @Test("persisted cursor does not suppress a fromSequence:0 full reload")
    func persistedCursorFullReload() async throws {
        let requests = Capture<String>()
        let server = try Self.historyServer(
            conversations: ["convC"], perConversation: 2, requests: requests)
        server.start()
        defer { server.stop() }

        // Simulates a relaunch: the SDK cursor remembers convC at 2 while
        // the app's in-memory list is empty (lastLoadedSequence == 0).
        let store = InMemorySequenceStore()
        store.set("airway-im.sequences", value: #"{"convC":2}"#)
        let im = AirwayIM(
            apiURL: server.baseURL, credential: "c",
            persistSequences: true, sequenceStore: store)

        let last = try await im.history("convC", fromSequence: 0)
        #expect(last.map(\.sequence) == [1, 2])
        #expect(requests.all.contains("convC?0"), "full reload must anchor at 0")
    }
}
