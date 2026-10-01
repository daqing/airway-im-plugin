import Foundation

/// Callbacks the gateway client reports to its owner (the session facade).
public struct GatewayCallbacks: Sendable {
    public var onEvent: @Sendable (GatewayEvent) -> Void
    public var onStatus: (@Sendable (ConnectionStatus) -> Void)?
    /// Fired after every successful (re)authentication; resync over HTTP here.
    public var onReady: (@Sendable () -> Void)?
    public var onError: (@Sendable (IMError) -> Void)?

    public init(
        onEvent: @escaping @Sendable (GatewayEvent) -> Void,
        onStatus: (@Sendable (ConnectionStatus) -> Void)? = nil,
        onReady: (@Sendable () -> Void)? = nil,
        onError: (@Sendable (IMError) -> Void)? = nil
    ) {
        self.onEvent = onEvent
        self.onStatus = onStatus
        self.onReady = onReady
        self.onError = onError
    }
}

/// WebSocket gateway client (deps/im/docs/design/gateway.md):
///   - connects to `<wsUrl>/ws` (`ws://` for local dev);
///   - the first application frame must be
///     `{"cmd":"auth","opts":["<credential>"]}` within the server's
///     10-second deadline — success replies `{"code":0,"data":"OK"}`,
///     failure closes with 1008;
///   - `{"cmd":"ping"}` answers `{"code":0,"data":"PONG"}`; the
///     application-level heartbeat runs every `pingInterval` (25 s
///     default, 0 disables); protocol-level ping/pong is handled by the
///     runtime;
///   - the server pushes at-least-once event frames; duplicates are
///     dropped by `event_id`.
///
/// Never rely on the socket for missed messages — after every
/// (re)connect, resync over HTTP (`after_sequence`), which the session's
/// sync engine does on `onReady`.
public actor GatewaySocket {
    private let callbacks: GatewayCallbacks
    private let socketFactory: @Sendable (URL) -> any WebSocketTransport
    private let pingInterval: TimeInterval
    private let getCredential: CredentialProvider?

    private var url: URL
    private var credential: String
    private var transport: (any WebSocketTransport)?
    private var runTask: Task<Void, Never>?
    private var pingTask: Task<Void, Never>?

    private var seenEventIds = Set<String>()
    private var reconnectDelayMs = 500
    private var closedByUser = false
    private var authenticated = false
    private var authRejected = false
    /// Credential value we already tried refreshing after an auth rejection.
    private var refreshedFor: String?

    private static let maxSeenEvents = 10_000
    private static let maxReconnectDelayMs = 10_000

    init(
        wsURL: String,
        credential: String,
        pingInterval: TimeInterval = 25,
        getCredential: CredentialProvider? = nil,
        socketFactory: @escaping @Sendable (URL) -> any WebSocketTransport,
        callbacks: GatewayCallbacks
    ) {
        var trimmed = wsURL.trimmingTrailingSlashes
        trimmed = Util.joinURL(trimmed, "/ws")
        self.url = URL(string: trimmed) ?? URL(string: "ws://invalid")!
        self.credential = credential
        self.pingInterval = pingInterval
        self.getCredential = getCredential
        self.socketFactory = socketFactory
        self.callbacks = callbacks
    }

    public func setCredential(_ credential: String) {
        self.credential = credential
    }

    public func setURL(_ wsURL: String) {
        url = URL(string: Util.joinURL(wsURL.trimmingTrailingSlashes, "/ws")) ?? url
    }

    public var isOnline: Bool {
        authenticated
    }

    /// Open the connection; idempotent. Failures surface through
    /// `callbacks.onError` and drive the reconnect loop.
    public func connect() {
        guard runTask == nil else { return }
        closedByUser = false
        refreshedFor = nil
        runTask = Task { await run() }
    }

    /// Close for good; no further reconnection until `connect()` again.
    public func close() {
        closedByUser = true
        stopPing()
        runTask?.cancel()
        runTask = nil
        let transport = self.transport
        self.transport = nil
        authenticated = false
        if let transport {
            Task { await transport.close() }
        }
        setStatus(.closed)
    }

    // ---- Run loop ----

    private func run() async {
        defer { runTask = nil }
        while !closedByUser {
            await openOnce()
            if closedByUser { break }

            if authRejected {
                authRejected = false
                if let getCredential, refreshedFor != credential {
                    // Refresh once per credential value; expired credentials
                    // recover automatically, permanently invalid ones stop
                    // the loop.
                    refreshedFor = credential
                    do {
                        let fresh = try await getCredential()
                        if !fresh.isEmpty, fresh != credential {
                            credential = fresh
                            refreshedFor = nil
                        }
                    } catch {
                        callbacks.onError?(IMError(
                            code: -1,
                            message: "credential refresh failed: \(error.localizedDescription)",
                            status: 0))
                    }
                    setStatus(.reconnecting)
                    continue // reconnect either way
                }
                setStatus(.offline)
                return
            }

            setStatus(.reconnecting)
            let delay = reconnectDelayMs
            reconnectDelayMs = min(reconnectDelayMs * 2, Self.maxReconnectDelayMs)
            try? await Task.sleep(nanoseconds: UInt64(delay) * 1_000_000)
        }
    }

    /// One connect → authenticate → receive cycle; returns when the socket
    /// closes, errors, or the credential is rejected.
    private func openOnce() async {
        authenticated = false
        authRejected = false
        setStatus(reconnectDelayMs > 500 ? .reconnecting : .connecting)

        let transport = socketFactory(url)
        self.transport = transport
        defer {
            self.transport = nil
        }

        do {
            try await transport.open(url)
            setStatus(.authenticating)
            try await transport.send(Self.string(authFrame()))

            stopPing()
            while !closedByUser, let text = try await transport.receive() {
                guard let frame = Self.parseFrame(text) else { continue } // ignore non-JSON frames
                if !authenticated {
                    if frame.code == 0 {
                        authenticated = true
                        reconnectDelayMs = 500
                        setStatus(.online)
                        startPing()
                        callbacks.onReady?()
                    } else {
                        // Auth rejected; the gateway closes with 1008 right
                        // after and the receive loop unwinds.
                        authRejected = true
                        callbacks.onError?(IMError(
                            code: -1,
                            message: "gateway auth failed: \(frame.message ?? text)",
                            status: 0))
                        break
                    }
                    continue
                }

                if frame.cmd != nil, frame.event == nil {
                    continue // command replies (e.g. PONG) carry no event payload
                }
                guard let event = frame.eventObject, !event.eventId.isEmpty else { continue }
                if seenEventIds.contains(event.eventId) { continue } // at-least-once dedupe
                if seenEventIds.count >= Self.maxSeenEvents { seenEventIds.removeAll() }
                seenEventIds.insert(event.eventId)
                callbacks.onEvent(event)
            }
        } catch is CancellationError {
            // close() cancelled us
        } catch {
            if !closedByUser, !authRejected {
                callbacks.onError?(IMError(
                    code: -1, message: error.localizedDescription, status: 0))
            }
        }

        stopPing()
        authenticated = false
        await transport.close()
    }

    private func authFrame() throws -> Data {
        try JSONSerialization.data(withJSONObject: ["cmd": "auth", "opts": [credential]])
    }

    private nonisolated static func string(_ data: Data) -> String {
        String(decoding: data, as: UTF8.self)
    }

    private func startPing() {
        stopPing()
        guard pingInterval > 0 else { return }
        let intervalNs = UInt64(pingInterval * 1_000_000_000)
        pingTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: intervalNs)
                guard let self, await self.isOnline else { continue }
                await self.sendPing()
            }
        }
    }

    private func stopPing() {
        pingTask?.cancel()
        pingTask = nil
    }

    private func sendPing() {
        guard let transport, authenticated else { return }
        Task {
            try? await transport.send(#"{"cmd":"ping"}"#)
        }
    }

    private func setStatus(_ status: ConnectionStatus) {
        callbacks.onStatus?(status)
    }
}

/// A loosely-parsed gateway frame.
private struct GatewayFrame {
    let cmd: String?
    let code: Int?
    let message: String?
    let event: String?
    let eventObject: GatewayEvent?

    init(_ object: [String: Any]) {
        cmd = object["cmd"] as? String
        code = object["code"] as? Int
        message = object["message"] as? String
        event = object["event"] as? String
        if event != nil {
            eventObject = try? Util.decoder.decode(GatewayEvent.self, from: JSONSerialization.data(withJSONObject: object))
        } else {
            eventObject = nil
        }
    }
}

private extension GatewaySocket {
    static func parseFrame(_ text: String) -> GatewayFrame? {
        guard
            let data = text.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data),
            let dictionary = object as? [String: Any]
        else { return nil }
        return GatewayFrame(dictionary)
    }
}
