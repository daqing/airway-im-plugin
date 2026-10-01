import Foundation

/// Callable the SDK invokes once when a credential is rejected
/// (HTTP 401 / code 10001) to fetch a fresh one from your backend.
public typealias CredentialProvider = @Sendable () async throws -> String

/// REST client for the Airway IM plugin public API (default port :1905).
/// Every request carries `Authorization: Bearer <host-signed credential>`;
/// responses are unwrapped from the `{code, data, message}` envelope and
/// decoded into typed values. Errors throw `IMError`.
///
/// Shared instances are safe to use concurrently (the type is an actor);
/// a successful credential renewal is a one-shot overwrite.
public actor RESTClient {
    private let transport: any HTTPTransport
    private var credential: String
    private let getCredential: CredentialProvider?
    private let baseURL: String

    init(
        baseURL: String,
        credential: String,
        getCredential: CredentialProvider? = nil,
        transport: (any HTTPTransport)? = nil
    ) {
        self.baseURL = baseURL.trimmingTrailingSlashes
        self.credential = credential
        self.getCredential = getCredential
        self.transport = transport ?? URLSessionTransport()
    }

    public func setCredential(_ credential: String) {
        self.credential = credential
    }

    public func currentCredential() -> String {
        credential
    }

    // ---- Identity ----

    public func me() async throws -> User {
        try await request(.get, "/api/v1/me")
    }

    // ---- Conversations ----

    /// List my active group conversations (direct ones are excluded by the
    /// backend).
    public func listGroups() async throws -> [ConversationSummary] {
        try await request(.get, "/api/v1/conversations", query: [("type", "group")])
    }

    /// Get-or-create the direct conversation with one other user, identified
    /// by their uuid.
    public func createDirect(otherUserUUID: String) async throws -> ConversationSummary {
        try await request(
            .post, "/api/v1/conversations",
            body: try Util.encoder.encode(
                CreateConversationBody(kind: "direct", memberUuids: [otherUserUUID], title: nil)))
    }

    /// The direct conversation with one other user by uuid, or nil when none
    /// exists yet (read-only; `createDirect` get-or-creates instead).
    public func getDirect(otherUserUUID: String) async throws -> ConversationSummary? {
        do {
            return try await request(
                .get, "/api/v1/conversations/direct/\(Util.escapeSegment(otherUserUUID))")
        } catch let error as IMError where error.code == ErrorCode.conversationNotFound {
            return nil
        }
    }

    /// Create a new group; the authenticated user becomes its owner.
    public func createGroup(title: String?, memberUUIDs: [String]) async throws -> ConversationSummary {
        try await request(
            .post, "/api/v1/group",
            body: try Util.encoder.encode(
                CreateConversationBody(kind: nil, memberUuids: memberUUIDs, title: title)))
    }

    /// Conversation kind plus active members with roles.
    public func getConversation(_ conversationId: String) async throws -> ConversationDetails {
        try await request(.get, "/api/v1/conversations/\(Util.escapeSegment(conversationId))")
    }

    /// Add members (by uuid) to a group (owner/admin; idempotent for
    /// already-active members).
    public func addMembers(
        _ conversationId: String,
        memberUUIDs: [String]
    ) async throws -> ConversationDetails {
        struct Body: Encodable { let memberUuids: [String] }
        return try await request(
            .post, "/api/v1/conversations/\(Util.escapeSegment(conversationId))/members",
            body: try Util.encoder.encode(Body(memberUuids: memberUUIDs)))
    }

    /// Remove members (by uuid) from a group (owner/admin; cannot remove
    /// self or the owner). Idempotent for members who are not active.
    /// Returns the details after the last removal (the current details for
    /// an empty list).
    public func removeMembers(
        _ conversationId: String,
        userUUIDs: [String]
    ) async throws -> ConversationDetails {
        guard !userUUIDs.isEmpty else {
            return try await getConversation(conversationId)
        }
        var details: ConversationDetails?
        for uuid in userUUIDs {
            details = try await request(
                .delete,
                "/api/v1/conversations/\(Util.escapeSegment(conversationId))/members/\(Util.escapeSegment(uuid))")
        }
        return details!
    }

    // ---- Messages ----

    /// Ordered message page after a sequence (limit 1-200; the backend
    /// falls back to 100 outside that range). Use for history display and
    /// reconnect synchronization.
    public func listMessages(
        _ conversationId: String,
        afterSequence: Int? = nil,
        limit: Int? = nil
    ) async throws -> [ChatMessage] {
        try await request(
            .get, "/api/v1/conversations/\(Util.escapeSegment(conversationId))/messages",
            query: [
                ("after_sequence", afterSequence.map(String.init)),
                ("limit", limit.map(String.init)),
            ])
    }

    /// Auto-paging over the conversation history in ascending sequence
    /// order; stops when a page comes back short or empty.
    public func eachMessage(
        _ conversationId: String,
        afterSequence: Int = 0,
        pageSize: Int = 100,
        _ handler: @Sendable (ChatMessage) -> Void
    ) async throws {
        let size = min(pageSize, 200)
        var cursor = afterSequence
        while true {
            let page = try await listMessages(conversationId, afterSequence: cursor, limit: size)
            if page.isEmpty { break }
            for message in page { handler(message) }
            cursor = page.last!.sequence
            if page.count < size { break }
        }
    }

    /// Send a message by conversation id. A random Idempotency-Key is
    /// generated per call and reused across network-failure retries, so
    /// retry storms can never duplicate a message; pass `idempotencyKey` to
    /// control it (1-128 chars, reuse only for the same logical request).
    public func sendMessage(
        _ conversationId: String,
        _ content: String,
        contentType: ContentType = .markdown,
        idempotencyKey: String? = nil,
        retries: Int = 1
    ) async throws -> ChatMessage {
        struct Body: Encodable {
            let conversationId: String
            let content: String
            let contentType: ContentType
        }
        let payload = try Util.encoder.encode(
            Body(conversationId: conversationId, content: content, contentType: contentType))
        let key = idempotencyKey ?? UUID().uuidString
        var attempt = 0
        while true {
            do {
                return try await request(
                    .post, "/api/v1/messages", body: payload,
                    headers: ["Idempotency-Key": key])
            } catch let error as IMError {
                attempt += 1
                // Retry transport failures (status 0) only; HTTP errors are
                // final and the backend dedupes by the reused key anyway.
                if error.status != 0 || attempt > retries { throw error }
            }
        }
    }

    /// Send a direct message to one other user, identified by their uuid:
    /// get-or-create the direct conversation, then send. Same idempotency
    /// semantics as `sendMessage`.
    public func sendDirectMessage(
        _ otherUserUUID: String,
        _ content: String,
        contentType: ContentType = .markdown,
        idempotencyKey: String? = nil,
        retries: Int = 1
    ) async throws -> ChatMessage {
        let conversation = try await createDirect(otherUserUUID: otherUserUUID)
        return try await sendMessage(
            conversation.id, content,
            contentType: contentType,
            idempotencyKey: idempotencyKey,
            retries: retries)
    }

    // ---- Storage ----

    /// Upload a file (development-stage API: currently no auth middleware).
    /// The whole file is read into memory.
    public func uploadFile(_ file: FileInput, dir: String? = nil) async throws -> UploadResult {
        let (data, filename): (Data, String)
        switch file {
        case .fileURL(let url):
            data = try Data(contentsOf: url)
            filename = url.lastPathComponent
        case .data(let bytes, let name):
            data = bytes
            filename = name
        }

        let boundary = "AirwayIM\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        var body = Data()
        func append(_ string: String) { body.append(Data(string.utf8)) }
        append("--\(boundary)\r\n")
        append("Content-Disposition: form-data; name=\"file\"; filename=\"\(sanitizeFilename(filename))\"\r\n")
        append("Content-Type: \(mimeType(for: filename))\r\n\r\n")
        body.append(data)
        append("\r\n")
        if let dir {
            append("--\(boundary)\r\nContent-Disposition: form-data; name=\"dir\"\r\n\r\n\(dir)\r\n")
        }
        append("--\(boundary)--\r\n")

        let response: HTTPResponse
        do {
            response = try await transport.send(HTTPRequest(
                url: URL(string: Util.joinURL(baseURL, "/api/v1/storage"))!,
                method: .post,
                headers: ["Content-Type": "multipart/form-data; boundary=\(boundary)"],
                body: body))
        } catch {
            throw IMError(code: -1, message: error.localizedDescription, status: 0)
        }

        if (200..<300).contains(response.status),
           let result = try? Util.decoder.decode(UploadResult.self, from: response.body) {
            return result
        }
        struct Failure: Decodable { let error: String? }
        let message = (try? Util.decoder.decode(Failure.self, from: response.body))?.error
            ?? "HTTP \(response.status): upload failed"
        throw IMError(code: -1, message: message, status: response.status)
    }

    /// Public download URL for a storage key (no auth, like the API itself).
    public nonisolated func storageURL(_ key: String) -> URL {
        URL(string: Util.joinURL(baseURL, "/api/v1/storage/\(Util.escapeSegments(key))"))!
    }

    // ---- Plumbing ----

    private struct CreateConversationBody: Encodable {
        // NB: single capitals — convertToSnakeCase would split a run like
        // "UUIDs" into "uui_ds".
        let kind: String?
        let memberUuids: [String]
        let title: String?
    }

    private func request<Value: Decodable>(
        _ method: HTTPMethod,
        _ path: String,
        query: [(String, String?)] = [],
        body: Data? = nil,
        headers: [String: String] = [:],
        auth: Bool = true
    ) async throws -> Value {
        do {
            return try await requestOnce(
                method, path, query: query, body: body, headers: headers, auth: auth)
        } catch let error as IMError {
            // Credential expired/revoked: refresh once through the host
            // callback and retry a single time. Other errors propagate
            // unchanged.
            guard
                error.isAuthError,
                auth,
                !credential.isEmpty,
                let getCredential
            else { throw error }
            let previous = credential
            let fresh = try await getCredential()
            guard !fresh.isEmpty, fresh != previous else { throw error }
            credential = fresh
            return try await requestOnce(
                method, path, query: query, body: body, headers: headers, auth: auth)
        }
    }

    private func requestOnce<Value: Decodable>(
        _ method: HTTPMethod,
        _ path: String,
        query: [(String, String?)],
        body: Data?,
        headers: [String: String],
        auth: Bool
    ) async throws -> Value {
        guard let url = Util.buildURL(base: baseURL, path: path, query: query) else {
            throw IMError(code: -1, message: "invalid URL for \(path)", status: 0)
        }
        var requestHeaders = headers
        if auth, !credential.isEmpty {
            requestHeaders["Authorization"] = "Bearer \(credential)"
        }
        if body != nil {
            requestHeaders["Content-Type"] = "application/json"
        }

        let response: HTTPResponse
        do {
            response = try await transport.send(
                HTTPRequest(url: url, method: method, headers: requestHeaders, body: body))
        } catch {
            // Transport failure: no HTTP status, safe to retry at a higher level.
            throw IMError(code: -1, message: error.localizedDescription, status: 0)
        }
        return try Self.unwrapEnvelope(Value.self, from: response)
    }

    /// Unwrap the `{code, data, message}` envelope: decode `data` on
    /// success, throw `IMError` otherwise. The envelope itself is parsed
    /// loosely (JSONSerialization) so `data` shapes stay fully controlled
    /// by the typed `Value`.
    static func unwrapEnvelope<Value: Decodable>(
        _ type: Value.Type, from response: HTTPResponse
    ) throws -> Value {
        guard
            let object = try? JSONSerialization.jsonObject(with: response.body),
            let envelope = object as? [String: Any],
            let code = envelope["code"] as? Int
        else {
            throw IMError(
                code: -1,
                message: "HTTP \(response.status): unexpected response",
                status: response.status)
        }
        if (200..<300).contains(response.status), code == 0 {
            // `data` may be absent, null, or a bare scalar — fragments are
            // allowed so those still decode.
            let data = envelope["data"] ?? NSNull()
            let payload = try JSONSerialization.data(
                withJSONObject: data, options: [.fragmentsAllowed])
            return try Util.decoder.decode(Value.self, from: payload)
        }
        let message = envelope["message"] as? String ?? "HTTP \(response.status)"
        throw IMError(code: code, message: message, status: response.status)
    }

    private func sanitizeFilename(_ filename: String) -> String {
        String(filename.map { "[\r\n\"\\\\]".contains($0) ? "_" : $0 })
    }

    private static let mimeTypes = [
        "png": "image/png",
        "jpg": "image/jpeg",
        "jpeg": "image/jpeg",
        "gif": "image/gif",
        "webp": "image/webp",
        "svg": "image/svg+xml",
        "pdf": "application/pdf",
        "txt": "text/plain",
        "md": "text/markdown",
        "mp4": "video/mp4",
        "mov": "video/quicktime",
        "mp3": "audio/mpeg",
        "zip": "application/zip",
        "json": "application/json",
    ]

    private func mimeType(for filename: String) -> String {
        let ext = (filename as NSString).pathExtension.lowercased()
        return Self.mimeTypes[ext] ?? "application/octet-stream"
    }
}
