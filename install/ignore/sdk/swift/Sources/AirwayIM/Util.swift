import Foundation

enum Util {
    /// JSON encoder matching the backend's snake_case bodies
    /// (member_uuids, conversation_id, content_type, …).
    static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        return encoder
    }()

    /// JSON decoder matching the backend's snake_case envelopes
    /// (avatar_url → avatarUrl, …).
    static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }()

    /// Identifiers are opaque strings; escape them so "/" or "?" in a value
    /// can never change the request path. RFC 3986 unreserved characters
    /// stay as-is (a space becomes %20, form encoding's "+" would change
    /// the value in a path).
    static func escapeSegment(_ value: String) -> String {
        let unreserved = CharacterSet(charactersIn:
            "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        return value.addingPercentEncoding(withAllowedCharacters: unreserved) ?? value
    }

    static func joinURL(_ base: String, _ path: String) -> String {
        let trimmedBase = base.trimmingTrailingSlashes
        let trimmedPath = path.trimmingLeadingSlashes
        return "\(trimmedBase)/\(trimmedPath)"
    }

    /// Query pairs keep insertion order so generated URLs are deterministic.
    static func buildURL(base: String, path: String, query: [(String, String?)] = []) -> URL? {
        var string = joinURL(base, path)
        let pairs = query.compactMap { (name, value) -> String? in
            guard let value else { return nil }
            return "\(escapeSegment(name))=\(escapeSegment(value))"
        }
        if !pairs.isEmpty { string += "?\(pairs.joined(separator: "&"))" }
        return URL(string: string)
    }

    static func escapeSegments(_ key: String) -> String {
        key.split(separator: "/", omittingEmptySubsequences: true)
            .map { escapeSegment(String($0)) }
            .joined(separator: "/")
    }
}

extension String {
    var trimmingTrailingSlashes: String {
        var copy = self
        while copy.hasSuffix("/") { copy.removeLast() }
        return copy
    }

    var trimmingLeadingSlashes: String {
        var copy = self
        while copy.hasPrefix("/") { copy.removeFirst() }
        return copy
    }
}
