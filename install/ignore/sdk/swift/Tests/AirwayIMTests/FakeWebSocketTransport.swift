import Foundation
import Testing
@testable import AirwayIM

/// Scriptable in-memory WebSocket transport for gateway tests: the test
/// plays the server by enqueuing text frames and inspecting what the
/// gateway sends.
actor FakeWebSocketTransport: WebSocketTransport {
    let sentTexts = Capture<String>()

    /// Hook invoked for every client frame, e.g. to answer the auth frame.
    nonisolated(unsafe) var onSend: (@Sendable (FakeWebSocketTransport, String) -> Void)?

    private var buffered: [String] = []
    private var waiters: [CheckedContinuation<String?, Never>] = []
    private var serverClosed = false

    init() {}

    static func make() -> FakeWebSocketTransport { FakeWebSocketTransport() }

    // ---- Client side (driven by the gateway) ----

    nonisolated func open(_ url: URL) async throws {}

    func send(_ text: String) async throws {
        sentTexts.append(text)
        onSend?(self, text)
    }

    func receive() async throws -> String? {
        if !buffered.isEmpty { return buffered.removeFirst() }
        if serverClosed { return nil }
        return await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }

    func close() async {
        serverClose()
    }

    // ---- Server side (test controls) ----

    func serverEnqueue(_ text: String) {
        if serverClosed { return }
        if let waiter = waiters.first {
            waiters.removeFirst()
            waiter.resume(returning: text)
        } else {
            buffered.append(text)
        }
    }

    func serverClose() {
        guard !serverClosed else { return }
        serverClosed = true
        let pending = waiters
        waiters.removeAll()
        for waiter in pending {
            waiter.resume(returning: nil)
        }
    }

    // ---- Convenience ----

    nonisolated func answerAuthOK() {
        onSend = { fake, text in
            if text.contains("\"auth\"") {
                Task { await fake.serverEnqueue(#"{"code":0,"data":"OK"}"#) }
            }
        }
    }

    nonisolated func answerAuthRejected(code: Int = 10001, message: String = "invalid credential") {
        onSend = { fake, text in
            if text.contains("\"auth\"") {
                Task { await fake.serverEnqueue(#"{"code":\#(code),"message":"\#(message)"}"#) }
            }
        }
    }
}

enum WaitForTimeout: Error { case timeout }

/// Polls until `condition` returns true or the timeout elapses.
func waitFor(
    _ label: String,
    timeout: Duration = .seconds(5),
    _ condition: @Sendable () async -> Bool
) async throws {
    let clock = ContinuousClock()
    let deadline = clock.now + timeout
    while !(await condition()) {
        if clock.now >= deadline {
            Issue.record("timeout waiting for: \(label)")
            return
        }
        try await Task.sleep(for: .milliseconds(20))
    }
}

/// Polls until `source` yields a value; throws after the timeout.
func waitForValue<T: Sendable>(
    _ label: String,
    timeout: Duration = .seconds(5),
    _ source: @Sendable () -> T?
) async throws -> T {
    let clock = ContinuousClock()
    let deadline = clock.now + timeout
    while true {
        if let value = source() { return value }
        if clock.now >= deadline {
            Issue.record("timeout waiting for: \(label)")
            throw WaitForTimeout.timeout
        }
        try await Task.sleep(for: .milliseconds(20))
    }
}

/// Builds a gateway event frame as a JSON string.
func eventFrame(
    eventId: String,
    event: String = GatewayEventType.messageCreated,
    conversationId: String,
    sequence: Int? = nil,
    messageId: String? = nil
) -> String {
    var object: [String: Any] = [
        "event_id": eventId,
        "event": event,
        "conversation_id": conversationId,
    ]
    if let sequence { object["sequence"] = sequence }
    if let messageId { object["message_id"] = messageId }
    let data = try! JSONSerialization.data(withJSONObject: object)
    return String(decoding: data, as: UTF8.self)
}
