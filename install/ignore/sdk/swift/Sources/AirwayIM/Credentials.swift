import Foundation
#if canImport(CryptoKit)
import CryptoKit

/// Airway-signed HMAC credentials (deps/im/docs/design/identity.md):
///
///     im1.<base64url(payload JSON)>.<base64url(HMAC-SHA256(secret, "im1." + payload))>
///
/// Use `sign` only when your backend is trusted with `IM_AUTH_SECRET` (that
/// is reserved for the Airway project's own side); third-party platform
/// backends never hold that secret and must use `InternalClient` /
/// `mintCredential` instead.
public enum Credentials {
    public static let prefix = "im1."
    public static let defaultTTL = 86400

    /// The decoded claims segment. Unknown fields are ignored; `iat`/`exp`
    /// are unix-seconds.
    public struct Claims: Codable, Sendable, Equatable {
        public var uuid: String
        public var name: String
        public var nickname: String?
        public var avatarUrl: String?
        public var tokenVersion: Int?
        public var iat: Int?
        public var exp: Int?

        public init(
            uuid: String,
            name: String,
            nickname: String? = nil,
            avatarUrl: String? = nil,
            tokenVersion: Int? = nil,
            iat: Int? = nil,
            exp: Int? = nil
        ) {
            self.uuid = uuid
            self.name = name
            self.nickname = nickname
            self.avatarUrl = avatarUrl
            self.tokenVersion = tokenVersion
            self.iat = iat
            self.exp = exp
        }

        enum CodingKeys: String, CodingKey {
            case uuid, name, nickname, iat, exp
            case avatarUrl = "avatar_url"
            case tokenVersion = "token_version"
        }
    }

    /// Sign `(uuid, name)` with the shared `IM_AUTH_SECRET`. Optional
    /// display fields are write-only-when-present: passing nil never
    /// clobbers a richer profile stored earlier in the IM users table.
    /// `ttl`: seconds until `exp`; nil omits the claim entirely (service
    /// credentials only). `now`: injectable clock, mainly for tests.
    public static func sign(
        secret: String,
        uuid: String,
        name: String,
        nickname: String? = nil,
        avatarUrl: String? = nil,
        tokenVersion: Int? = nil,
        ttl: Int? = defaultTTL,
        now: Date = Date()
    ) throws -> String {
        try validateLength(uuid, 1, 64, field: "uuid")
        try validateLength(name, 1, 64, field: "name")
        if let nickname { try validateLength(nickname, 1, 64, field: "nickname") }
        if let avatarUrl { try validateLength(avatarUrl, 1, 2048, field: "avatarUrl") }
        if let tokenVersion, tokenVersion < 1 {
            throw IMError(code: -1, message: "tokenVersion must be >= 1", status: 0)
        }

        var claims = Claims(uuid: uuid, name: name)
        claims.nickname = nickname
        claims.avatarUrl = avatarUrl
        claims.tokenVersion = tokenVersion
        let issuedAt = Int(now.timeIntervalSince1970)
        claims.iat = issuedAt
        claims.exp = ttl.map { issuedAt + $0 }

        // Ordered, compact claims JSON: the signature covers the exact
        // bytes, so the key order must match the Node reference
        // (uuid, name, nickname, avatar_url, token_version, iat, exp) —
        // JSONEncoder does not preserve property order.
        let payload = base64URL(Data(claimsJSON(claims).utf8))
        let mac = HMAC<SHA256>.authenticationCode(
            for: Data("\(prefix)\(payload)".utf8),
            using: SymmetricKey(data: Data(secret.utf8)))
        let signature = base64URL(Data(mac))
        return "\(prefix)\(payload).\(signature)"
    }

    /// Decode the claims segment. Throws `IMError` (code -1) on malformed
    /// input.
    public static func decode(_ credential: String) throws -> Claims {
        let payload = try segments(credential)[1]
        guard let data = dataFromBase64URL(payload) else {
            throw IMError(code: -1, message: "malformed credential: invalid payload", status: 0)
        }
        do {
            return try JSONDecoder().decode(Claims.self, from: data)
        } catch {
            throw IMError(code: -1, message: "malformed credential: invalid payload", status: 0)
        }
    }

    /// Recompute the HMAC over the received payload and compare in constant
    /// time. Never throws on malformed input, returns false instead.
    public static func verifySignature(_ credential: String, secret: String) -> Bool {
        guard let parts = try? segments(credential),
              let signature = dataFromBase64URL(parts[2])
        else { return false }
        let key = SymmetricKey(data: Data(secret.utf8))
        let message = Data("\(parts[0]).\(parts[1])".utf8)
        return HMAC<SHA256>.isValidAuthenticationCode(signature, authenticating: message, using: key)
    }

    /// Whether `exp` has passed; a credential without `exp` never expires
    /// here (the backend treats it the same way).
    public static func isExpired(_ credential: String, now: Date = Date()) -> Bool {
        guard let claims = try? decode(credential) else { return false }
        return isExpired(claims, now: now)
    }

    public static func isExpired(_ claims: Claims, now: Date = Date()) -> Bool {
        guard let exp = claims.exp else { return false }
        return exp <= Int(now.timeIntervalSince1970)
    }

    // ---- Plumbing ----

    /// Splits into exactly `im1.<payload>.<signature>`; throws otherwise.
    private static func segments(_ credential: String) throws -> [String] {
        let parts = credential.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 3, parts[0] == "im1" else {
            throw IMError(
                code: -1,
                message: "malformed credential: expected im1.<payload>.<signature>",
                status: 0)
        }
        return parts
    }

    /// Compact claims JSON in the reference key order, with optional fields
    /// written only when present.
    private static func claimsJSON(_ claims: Claims) -> String {
        var parts: [String] = [
            "\"uuid\":\(jsonString(claims.uuid))",
            "\"name\":\(jsonString(claims.name))",
        ]
        if let nickname = claims.nickname {
            parts.append("\"nickname\":\(jsonString(nickname))")
        }
        if let avatarUrl = claims.avatarUrl {
            parts.append("\"avatar_url\":\(jsonString(avatarUrl))")
        }
        if let tokenVersion = claims.tokenVersion {
            parts.append("\"token_version\":\(tokenVersion)")
        }
        if let iat = claims.iat {
            parts.append("\"iat\":\(iat)")
        }
        if let exp = claims.exp {
            parts.append("\"exp\":\(exp)")
        }
        return "{\(parts.joined(separator: ","))}"
    }

    /// JSON-escaped string literal (quotes included).
    private static func jsonString(_ value: String) -> String {
        let data = try? JSONSerialization.data(withJSONObject: value, options: [.fragmentsAllowed])
        return data.map { String(decoding: $0, as: UTF8.self) } ?? "\"\(value)\""
    }

    private static func validateLength(_ value: String?, _ min: Int, _ max: Int, field: String) throws {
        guard let value, (min...max).contains(value.count) else {
            throw IMError(code: -1, message: "\(field) must be \(min)-\(max) characters", status: 0)
        }
    }

    private static func base64URL(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    private static func dataFromBase64URL(_ string: String) -> Data? {
        var base64 = string
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder > 0 {
            base64.append(String(repeating: "=", count: 4 - remainder))
        }
        return Data(base64Encoded: base64)
    }
}

#endif
