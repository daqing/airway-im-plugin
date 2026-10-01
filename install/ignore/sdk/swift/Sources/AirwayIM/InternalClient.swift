import Foundation

/// A freshly minted credential.
public struct MintedCredential: Sendable, Equatable {
    /// The finished `im1.<payload>.<sig>` token: hand it to the client with
    /// your login response and pass it to `AirwayIM(credential:)` /
    /// `setCredential(_:)`.
    public let credential: String
    /// RFC 3339 UTC timestamp; nil when `ttlSeconds` was 0 (no expiry claim).
    public let expiresAt: String?
}

/// Server-to-server client for the plugin's internal API listener
/// (`IM_INTERNAL_ADDR`, default 127.0.0.1:1906, `X-IM-Internal-Secret`
/// protected). Reachable only from a private network — never from end-user
/// clients: whoever can mint credentials can impersonate any user, so do
/// not link this type into client-facing code paths.
public struct InternalClient: Sendable {
    private let transport: any HTTPTransport
    private let internalSecret: String
    private let baseURL: String

    /// - Parameters:
    ///   - internalURL: Internal listener URL, e.g. http://127.0.0.1:1906 —
    ///     private network only.
    ///   - internalSecret: The deployment's `IM_INTERNAL_SECRET`.
    public init(
        internalURL: String,
        internalSecret: String,
        timeout: TimeInterval = 15,
        transport: (any HTTPTransport)? = nil
    ) {
        self.baseURL = internalURL.trimmingTrailingSlashes
        self.internalSecret = internalSecret
        self.transport = transport ?? URLSessionTransport(timeout: timeout)
    }

    /// Mint a client credential for `(uuid, name)` server-to-server. The
    /// user is registered with IM on the first mint under that uuid (the
    /// same uuid your clients pass to `createDirect` / `sendDirectMessage`);
    /// the credential carries the user's current `token_version`, so
    /// revoking the user through the admin API invalidates every credential
    /// minted before it.
    ///
    /// Optional display fields are write-only-when-present: omitting them
    /// never clobbers a richer profile stored earlier in the IM users
    /// table. `ttlSeconds` defaults to 86400 (24 h) on the backend and is
    /// capped at 2592000 (30 days); 0 omits the expiry claim (service
    /// credentials only).
    public func mintCredential(
        uuid: String,
        name: String,
        nickname: String? = nil,
        avatarUrl: String? = nil,
        ttlSeconds: Int? = nil
    ) async throws -> MintedCredential {
        struct Body: Encodable {
            let uuid: String
            let name: String
            let nickname: String?
            let avatarUrl: String?
            let ttlSeconds: Int?
        }
        struct MintData: Decodable {
            let credential: String
            let expiresAt: String?
        }
        let payload = try Util.encoder.encode(Body(
            uuid: uuid,
            name: name,
            nickname: nickname,
            avatarUrl: avatarUrl,
            ttlSeconds: ttlSeconds))

        let response: HTTPResponse
        do {
            response = try await transport.send(HTTPRequest(
                url: URL(string: Util.joinURL(baseURL, "/internal/v1/credentials"))!,
                method: .post,
                headers: [
                    "Content-Type": "application/json",
                    "X-IM-Internal-Secret": internalSecret,
                ],
                body: payload))
        } catch {
            // Transport failure (including timeout): no HTTP status, the
            // caller may retry.
            throw IMError(code: -1, message: error.localizedDescription, status: 0)
        }
        let data = try RESTClient.unwrapEnvelope(MintData.self, from: response)
        return MintedCredential(credential: data.credential, expiresAt: data.expiresAt)
    }
}
