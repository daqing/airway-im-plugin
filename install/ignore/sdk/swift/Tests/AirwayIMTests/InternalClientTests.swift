import Testing
import Foundation
@testable import AirwayIM

@Suite("InternalClient")
struct InternalClientTests {
    @Test("mintCredential posts to the internal listener with the secret header")
    func mint() async throws {
        let bodies = Capture<[String: Any]>()
        let auths = Capture<String>()
        let paths = Capture<String>()
        let server = try StubHTTPServer(handler: { request in
            paths.append(request.path)
            auths.append(request.headers["x-im-internal-secret"] ?? "")
            bodies.append(try request.jsonBody())
            return StubResponse(json: envelope([
                "credential": "im1.abc.def",
                "expires_at": "2026-09-26T00:00:00Z",
            ]))
        })
        server.start()
        defer { server.stop() }

        let client = InternalClient(internalURL: server.baseURL, internalSecret: "s3cret")
        let minted = try await client.mintCredential(
            uuid: "user-42",
            name: "alice",
            nickname: "Alice",
            avatarUrl: "https://example.com/a.png",
            ttlSeconds: 86_400)

        #expect(paths.all == ["/internal/v1/credentials"])
        #expect(auths.all == ["s3cret"])
        let body = bodies.last ?? [:]
        #expect(body["uuid"] as? String == "user-42")
        #expect(body["name"] as? String == "alice")
        #expect(body["nickname"] as? String == "Alice")
        #expect(body["avatar_url"] as? String == "https://example.com/a.png")
        #expect(body["ttl_seconds"] as? Int == 86_400)
        #expect(minted.credential == "im1.abc.def")
        #expect(minted.expiresAt == "2026-09-26T00:00:00Z")
    }

    @Test("mintCredential omits absent optional fields")
    func mintOmitsAbsentFields() async throws {
        let bodies = Capture<[String: Any]>()
        let server = try StubHTTPServer(handler: { request in
            bodies.append(try request.jsonBody())
            return StubResponse(json: envelope(["credential": "im1.x.y"]))
        })
        server.start()
        defer { server.stop() }

        let client = InternalClient(internalURL: server.baseURL, internalSecret: "s")
        let minted = try await client.mintCredential(uuid: "u1", name: "bob")
        #expect(minted.credential == "im1.x.y")
        #expect(minted.expiresAt == nil)
        let body = bodies.last ?? [:]
        #expect(body["nickname"] == nil)
        #expect(body["avatar_url"] == nil)
        #expect(body["ttl_seconds"] == nil)
    }

    @Test("mint errors surface the envelope code (10005 on a wrong secret)")
    func mintWrongSecret() async throws {
        let server = try StubHTTPServer(handler: { _ in
            StubResponse(json: envelope(nil, code: 10005, message: "invalid internal secret"))
                .with(status: 403)
        })
        server.start()
        defer { server.stop() }

        let client = InternalClient(internalURL: server.baseURL, internalSecret: "wrong")
        do {
            _ = try await client.mintCredential(uuid: "u1", name: "bob")
            Issue.record("expected an error")
        } catch let error as IMError {
            #expect(error.code == 10005)
            #expect(error.status == 403)
        }
    }
}

@Suite("AdminClient")
struct AdminClientTests {
    private func makeServer(
        requests: Capture<String>,
        logins: Capture<Bool>,
        tokenSeen: Capture<String>,
        rejectNext: LockedBox<Bool> = LockedBox(false)
    ) throws -> StubHTTPServer {
        let server = try StubHTTPServer(handler: { request in
            requests.append("\(request.method) \(request.target)")
            if request.path == "/admin/api/login" {
                logins.append(true)
                return StubResponse(json: envelope([
                    "token": "session-1", "expires_at": "later", "username": "admin",
                ]))
            }
            tokenSeen.append(request.headers["authorization"] ?? "")
            if rejectNext.current {
                // Reject exactly one protected call: the session token was
                // lost/expired server-side.
                rejectNext.set(false)
                return StubResponse(json: envelope(nil, code: 10001, message: "session expired"))
                    .with(status: 401)
            }
            if request.path == "/admin/api/status" {
                return StubResponse(json: envelope(["users": 2, "online": 1]))
            }
            if request.path == "/admin/api/users" {
                return StubResponse(json: envelope([["uuid": "user-42", "token_version": 1]]))
            }
            if request.path.hasPrefix("/admin/api/users/user-42/revoke") {
                return StubResponse(json: envelope(["uuid": "user-42", "token_version": 2, "connections_kicked": 1]))
            }
            if request.path == "/admin/api/conversations" {
                return StubResponse(json: envelope([["conversation_uuid": "01AB", "members": 2]]))
            }
            if request.path.hasSuffix("/messages") {
                return StubResponse(json: envelope([["id": "01J", "sequence": 1]]))
            }
            if request.path.contains("mark-illegal") {
                return StubResponse(json: envelope(["id": "01J", "masked": true]))
            }
            return StubResponse(json: envelope(nil))
        })
        return server
    }

    @Test("the first authenticated call logs in transparently")
    func implicitLogin() async throws {
        let requests = Capture<String>()
        let logins = Capture<Bool>()
        let tokenSeen = Capture<String>()
        let server = try makeServer(requests: requests, logins: logins, tokenSeen: tokenSeen)
        server.start()
        defer { server.stop() }

        let admin = AdminClient(apiURL: server.baseURL, username: "admin", password: "pw")
        let status = try await admin.status()
        #expect(status["users"]?.intValue == 2)
        #expect(logins.count == 1)
        #expect(tokenSeen.all == ["Bearer session-1"])

        _ = try await admin.users()
        #expect(logins.count == 1) // session reused
    }

    @Test("a mid-session 401 triggers one re-login and retry")
    func reloginOn401() async throws {
        let requests = Capture<String>()
        let logins = Capture<Bool>()
        let tokenSeen = Capture<String>()
        let rejectNext = LockedBox(false)
        let server = try makeServer(
            requests: requests, logins: logins, tokenSeen: tokenSeen, rejectNext: rejectNext)
        server.start()
        defer { server.stop() }

        let admin = AdminClient(apiURL: server.baseURL, username: "admin", password: "pw")
        _ = try await admin.status()
        #expect(logins.count == 1)

        // Expire the session server-side: the next call 401s once, which
        // triggers one re-login and a successful retry.
        rejectNext.set(true)
        _ = try await admin.revokeUser("user-42")
        #expect(logins.count == 2)
        #expect(tokenSeen.all == ["Bearer session-1", "Bearer session-1", "Bearer session-1"])
    }

    @Test("all admin endpoints hit their documented paths")
    func endpointPaths() async throws {
        let requests = Capture<String>()
        let logins = Capture<Bool>()
        let tokenSeen = Capture<String>()
        let server = try makeServer(requests: requests, logins: logins, tokenSeen: tokenSeen)
        server.start()
        defer { server.stop() }

        let admin = AdminClient(apiURL: server.baseURL, username: "admin", password: "pw")
        _ = try await admin.users()
        _ = try await admin.revokeUser("user 42")
        _ = try await admin.groupConversations()
        _ = try await admin.conversationMessages("01AB")
        _ = try await admin.markIllegal("01J")
        try await admin.logout()

        #expect(requests.all.contains("POST /admin/api/users/user%2042/revoke"))
        #expect(requests.all.contains("GET /admin/api/conversations/01AB/messages"))
        #expect(requests.all.contains("POST /admin/api/messages/01J/mark-illegal"))
        #expect(requests.all.last == "POST /admin/api/logout")
    }
}
