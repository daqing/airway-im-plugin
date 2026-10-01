import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// The socket seam the gateway client talks to. The default
/// URLSessionWebSocketTransport wraps `URLSessionWebSocketTask`; tests and
/// alternative runtimes plug in their own implementation.
public protocol WebSocketTransport: Sendable {
    /// Bring the socket up; returns once the transport is ready for
    /// `send`/`receive`. Throws when the connection cannot be established.
    func open(_ url: URL) async throws

    /// Send one text frame.
    func send(_ text: String) async throws

    /// Wait for the next text frame; nil means the socket closed.
    func receive() async throws -> String?

    /// Close for good; unblocks a pending `receive`.
    func close() async
}

/// Default transport built on `URLSessionWebSocketTask` (iOS 13+/macOS
/// 10.15+). Protocol-level ping/pong frames are answered by the runtime.
public struct URLSessionWebSocketTransport: WebSocketTransport {
    private let task: URLSessionWebSocketTask
    private let session: URLSession

    public init(url: URL, timeout: TimeInterval = 15) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeout
        configuration.httpShouldSetCookies = false
        session = URLSession(configuration: configuration)
        task = session.webSocketTask(with: url)
    }

    public func open(_ url: URL) async throws {
        task.resume()
    }

    public func send(_ text: String) async throws {
        try await task.send(.string(text))
    }

    public func receive() async throws -> String? {
        while true {
            let message = try await task.receive()
            switch message {
            case .string(let text):
                return text
            case .data:
                continue // binary frames are not part of the protocol
            @unknown default:
                continue
            }
        }
    }

    public func close() async {
        task.cancel(with: .goingAway, reason: nil)
        session.finishTasksAndInvalidate()
    }
}
