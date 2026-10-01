import Foundation

/// The admin login payload.
public struct AdminSession: Sendable, Equatable {
    public let token: String
    public let expiresAt: String?
    public let username: String?
}

/// Client for the admin console API (`/admin/api` on the public listener).
/// Authenticates with `IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD` session
/// tokens (12-hour in-memory sessions): the first authenticated call logs
/// in transparently, and a 401 mid-session triggers one re-login and retry.
///
/// This is an operational API, not a client API — never expose it publicly
/// without network-level access control and TLS. Payloads are returned as
/// `JSONValue` (the admin shapes are open-ended; see
/// deps/im/docs/api/admin.md).
public actor AdminClient {
    private let transport: any HTTPTransport
    private let username: String
    private let password: String
    private let baseURL: String
    private var token: String?

    public init(
        apiURL: String,
        username: String,
        password: String,
        timeout: TimeInterval = 15,
        transport: (any HTTPTransport)? = nil
    ) {
        self.baseURL = apiURL.trimmingTrailingSlashes
        self.username = username
        self.password = password
        self.transport = transport ?? URLSessionTransport(timeout: timeout)
    }

    /// Exchange admin credentials for a session token (also happens
    /// implicitly before the first authenticated call).
    @discardableResult
    public func login() async throws -> AdminSession {
        struct Body: Encodable {
            let username: String
            let password: String
        }
        struct LoginData: Decodable {
            let token: String
            let expiresAt: String?
            let username: String?
        }
        let data: LoginData = try await call(
            .post, "/admin/api/login",
            json: try Util.encoder.encode(Body(username: username, password: password)),
            auth: false)
        token = data.token
        return AdminSession(token: data.token, expiresAt: data.expiresAt, username: data.username)
    }

    /// Revoke the current session.
    public func logout() async throws {
        let _: JSONValue = try await call(.post, "/admin/api/logout")
        token = nil
    }

    /// Aggregated status: user, outbox, gateway, and delivery metrics.
    public func status() async throws -> JSONValue {
        try await call(.get, "/admin/api/status")
    }

    /// Registered identities, newest first, with `token_version`.
    public func users() async throws -> [JSONValue] {
        try await call(.get, "/admin/api/users")
    }

    /// Revoke a user's outstanding credentials (bumps `token_version`) and
    /// best-effort kick their live gateway connections. An unknown uuid
    /// returns 404; revocation is not a ban.
    public func revokeUser(_ uuid: String) async throws -> JSONValue {
        try await call(.post, "/admin/api/users/\(Util.escapeSegment(uuid))/revoke")
    }

    /// All group conversations with member/message counts.
    public func groupConversations() async throws -> [JSONValue] {
        try await call(.get, "/admin/api/conversations")
    }

    /// All messages in a conversation, ascending sequence order.
    public func conversationMessages(_ conversationId: String) async throws -> [JSONValue] {
        try await call(
            .get, "/admin/api/conversations/\(Util.escapeSegment(conversationId))/messages")
    }

    /// Mark a message illegal (idempotent): masked to `***` for clients,
    /// and a `message.moderated` event fans out to online members.
    public func markIllegal(_ messageId: String) async throws -> JSONValue {
        try await call(.post, "/admin/api/messages/\(Util.escapeSegment(messageId))/mark-illegal")
    }

    // ---- Plumbing ----

    private func call<Value: Decodable>(
        _ method: HTTPMethod,
        _ path: String,
        json: Data? = nil,
        auth: Bool = true
    ) async throws -> Value {
        if auth { try await ensureLoggedIn() }
        do {
            return try await perform(method, path, json: json, auth: auth)
        } catch let error as IMError where auth && error.status == 401 {
            // The 12-hour session token expired (or was lost server-side):
            // re-login once and retry a single time.
            token = nil
            try await ensureLoggedIn()
            return try await perform(method, path, json: json, auth: auth)
        }
    }

    private func perform<Value: Decodable>(
        _ method: HTTPMethod,
        _ path: String,
        json: Data?,
        auth: Bool
    ) async throws -> Value {
        var headers: [String: String] = [:]
        if json != nil {
            headers["Content-Type"] = "application/json"
        }
        if auth {
            headers["Authorization"] = "Bearer \(token ?? "")"
        }
        guard let url = Util.buildURL(base: baseURL, path: path) else {
            throw IMError(code: -1, message: "invalid URL for \(path)", status: 0)
        }
        let response: HTTPResponse
        do {
            response = try await transport.send(
                HTTPRequest(url: url, method: method, headers: headers, body: json))
        } catch {
            throw IMError(code: -1, message: error.localizedDescription, status: 0)
        }
        return try RESTClient.unwrapEnvelope(Value.self, from: response)
    }

    private func ensureLoggedIn() async throws {
        if token == nil { _ = try await login() }
    }
}
