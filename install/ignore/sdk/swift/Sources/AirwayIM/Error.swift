// Business codes from the backend's {code, data, message} envelope
// (deps/im/docs/api/openapi.md).

public enum ErrorCode {
    public static let internalError = 10000
    public static let invalidCredential = 10001
    public static let invalidRequest = 10003
    public static let permissionDenied = 10005
    public static let conversationNotFound = 11001
    public static let idempotencyKeyReused = 11002
}

/// The error type every SDK call throws: `code` is the envelope business
/// code (10000-11002, -1 when the body was not an envelope or the failure
/// happened before a response arrived), `status` the HTTP status code
/// (0 for transport failures: network error, timeout).
public struct IMError: Error, Sendable, CustomStringConvertible {
    public let code: Int
    public let status: Int
    public let message: String

    public init(code: Int, message: String, status: Int) {
        self.code = code
        self.message = message
        self.status = status
    }

    /// The credential is missing, malformed, revoked, or expired.
    public var isAuthError: Bool {
        code == ErrorCode.invalidCredential || status == 401
    }

    public var description: String {
        "IMError(code: \(code), status: \(status), message: \(message))"
    }
}
