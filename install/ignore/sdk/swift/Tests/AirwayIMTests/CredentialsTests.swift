import Testing
import Foundation
@testable import AirwayIM

@Suite("Credentials")
struct CredentialsTests {
    static let secret = "test-secret"

    /// Cross-language regression vector generated with the Node.js
    /// reference implementation from deps/im/docs/design/identity.md §4.2:
    /// claims {uuid: "user-42", name: "alice", iat: 1700000000, exp: 1700086400}.
    static let nodeVector = "im1.eyJ1dWlkIjoidXNlci00MiIsIm5hbWUiOiJhbGljZSIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDg2NDAwfQ.w_i4UIKCVjBY0XwnDdeRsnOCHoqgxxl1rDAESDh7VeU"

    @Test("sign matches the Node reference vector byte for byte")
    func signMatchesNodeVector() throws {
        let credential = try Credentials.sign(
            secret: Self.secret,
            uuid: "user-42",
            name: "alice",
            ttl: 86_400,
            now: Date(timeIntervalSince1970: 1_700_000_000))
        #expect(credential == Self.nodeVector)
    }

    @Test("sign includes optional fields when present")
    func signIncludesOptionalFields() throws {
        let credential = try Credentials.sign(
            secret: Self.secret,
            uuid: "u1",
            name: "bob",
            nickname: "Bob",
            avatarUrl: "https://example.com/bob.png",
            tokenVersion: 3,
            ttl: 60,
            now: Date(timeIntervalSince1970: 1_700_000_000))
        let claims = try Credentials.decode(credential)
        #expect(claims == Credentials.Claims(
            uuid: "u1",
            name: "bob",
            nickname: "Bob",
            avatarUrl: "https://example.com/bob.png",
            tokenVersion: 3,
            iat: 1_700_000_000,
            exp: 1_700_000_060))
    }

    @Test("sign omits nil optional fields")
    func signOmitsNilFields() throws {
        let credential = try Credentials.sign(
            secret: Self.secret,
            uuid: "u1",
            name: "bob",
            now: Date(timeIntervalSince1970: 0))
        let claims = try Credentials.decode(credential)
        #expect(claims.uuid == "u1")
        #expect(claims.name == "bob")
        #expect(claims.nickname == nil)
        #expect(claims.iat == 0)
        #expect(claims.exp == 86_400)
    }

    @Test("sign omits exp when ttl is nil")
    func signOmitsExpWhenTTLNil() throws {
        let credential = try Credentials.sign(
            secret: Self.secret,
            uuid: "u1",
            name: "bob",
            ttl: nil,
            now: Date(timeIntervalSince1970: 0))
        let claims = try Credentials.decode(credential)
        #expect(claims.exp == nil)
        #expect(Credentials.verifySignature(credential, secret: Self.secret))
    }

    @Test("sign rejects out-of-range fields")
    func signRejectsOutOfRangeFields() {
        #expect(throws: IMError.self) {
            _ = try Credentials.sign(secret: Self.secret, uuid: String(repeating: "x", count: 65), name: "bob")
        }
        #expect(throws: IMError.self) {
            _ = try Credentials.sign(secret: Self.secret, uuid: "u1", name: "")
        }
        #expect(throws: IMError.self) {
            _ = try Credentials.sign(secret: Self.secret, uuid: "u1", name: "bob", nickname: String(repeating: "x", count: 65))
        }
        #expect(throws: IMError.self) {
            _ = try Credentials.sign(secret: Self.secret, uuid: "u1", name: "bob", avatarUrl: String(repeating: "x", count: 2049))
        }
        #expect(throws: IMError.self) {
            _ = try Credentials.sign(secret: Self.secret, uuid: "u1", name: "bob", tokenVersion: 0)
        }
    }

    @Test("decode round-trips the node vector")
    func decodeRoundTrip() throws {
        let claims = try Credentials.decode(Self.nodeVector)
        #expect(claims.uuid == "user-42")
        #expect(claims.name == "alice")
        #expect(claims.iat == 1_700_000_000)
        #expect(claims.exp == 1_700_086_400)
    }

    @Test("decode rejects malformed input")
    func decodeRejectsMalformedInput() {
        for bad in ["", "not-a-credential", "im2.aaa.bbb", "im1.aaa", "im1.aaa.bbb.ccc"] {
            #expect(throws: IMError.self) {
                _ = try Credentials.decode(bad)
            }
        }
    }

    @Test("verifySignature accepts valid and rejects tampered")
    func verifySignature() {
        #expect(Credentials.verifySignature(Self.nodeVector, secret: Self.secret))
        #expect(!Credentials.verifySignature(Self.nodeVector, secret: "wrong-secret"))

        let parts = Self.nodeVector.split(separator: ".").map(String.init)
        var tampered = parts
        tampered[1] = (parts[1].hasPrefix("A") ? "B" : "A") + String(parts[1].dropFirst())
        #expect(!Credentials.verifySignature(tampered.joined(separator: "."), secret: Self.secret))
        #expect(!Credentials.verifySignature("", secret: Self.secret))
        #expect(!Credentials.verifySignature("garbage", secret: Self.secret))
    }

    @Test("expiry follows the exp claim")
    func expiry() throws {
        #expect(!Credentials.isExpired(Self.nodeVector, now: Date(timeIntervalSince1970: 1_700_086_399)))
        #expect(Credentials.isExpired(Self.nodeVector, now: Date(timeIntervalSince1970: 1_700_086_400)))

        let never = try Credentials.sign(
            secret: Self.secret, uuid: "u1", name: "bob", ttl: nil,
            now: Date(timeIntervalSince1970: 0))
        #expect(!Credentials.isExpired(never, now: Date(timeIntervalSince1970: 4_102_444_800)))
    }
}
