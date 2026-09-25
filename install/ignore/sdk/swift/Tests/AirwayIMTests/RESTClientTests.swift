import Testing
import Foundation
@testable import AirwayIM

@Suite("RESTClient")
struct RESTClientTests {
    private func withServer(
        _ handler: @escaping StubHTTPServer.Handler,
        credential: String = "cred-1",
        getCredential: CredentialProvider? = nil,
        _ body: (StubHTTPServer, RESTClient) async throws -> Void
    ) async throws {
        let server = try StubHTTPServer(handler: handler)
        server.start()
        defer { server.stop() }
        let client = RESTClient(
            baseURL: server.baseURL,
            credential: credential,
            getCredential: getCredential)
        try await body(server, client)
    }

    @Test("me() unwraps the envelope")
    func me() async throws {
        try await withServer({ request in
            #expect(request.method == "GET")
            #expect(request.path == "/api/v1/me")
            #expect(request.headers["authorization"] == "Bearer cred-1")
            return StubResponse(json: envelope([
                "uuid": "user-42",
                "username": "alice",
                "nickname": "Alice",
                "avatar_url": "https://example.com/a.png",
                "email": nil,
                "last_seen_at": nil,
                "created_at": "2026-01-01T00:00:00Z",
                "updated_at": "2026-01-01T00:00:00Z",
            ]))
        }, { _, client in
            let user = try await client.me()
            #expect(user.uuid == "user-42")
            #expect(user.username == "alice")
            #expect(user.nickname == "Alice")
            #expect(user.email == nil)
        })
    }

    @Test("envelope errors become IMError with code and status")
    func envelopeError() async throws {
        try await withServer({ _ in
            StubResponse(json: envelope(nil, code: 10005, message: "not a member"))
                .with(status: 403)
        }, { _, client in
            do {
                _ = try await client.listGroups()
                Issue.record("expected an error")
            } catch let error as IMError {
                #expect(error.code == 10005)
                #expect(error.status == 403)
                #expect(error.message == "not a member")
                #expect(!error.isAuthError)
            }
        })
    }

    @Test("a non-envelope body maps to code -1 with the HTTP status")
    func nonEnvelopeBody() async throws {
        try await withServer({ _ in
            StubResponse(status: 500, body: Data("gateway timeout".utf8))
        }, { _, client in
            do {
                _ = try await client.listGroups()
                Issue.record("expected an error")
            } catch let error as IMError {
                #expect(error.code == -1)
                #expect(error.status == 500)
            }
        })
    }

    @Test("getDirect returns nil on 11001")
    func getDirectAbsent() async throws {
        try await withServer({ _ in
            StubResponse(json: envelope(nil, code: 11001, message: "conversation not found"))
                .with(status: 404)
        }, { _, client in
            let summary = try await client.getDirect(otherUserUUID: "user-b")
            #expect(summary == nil)
        })
    }

    @Test("createDirect sends kind and member_uuids")
    func createDirectBody() async throws {
        try await withServer({ request in
            let body = try request.jsonBody()
            #expect(body["kind"] as? String == "direct")
            #expect(body["member_uuids"] as? [String] == ["user-b"])
            return StubResponse(json: envelope([
                "id": "01AB", "kind": "direct", "title": nil, "avatar_url": nil,
                "created_by": "user-42", "created_at": "t1", "updated_at": "t1",
            ]))
        }, { _, client in
            let summary = try await client.createDirect(otherUserUUID: "user-b")
            #expect(summary.id == "01AB")
            #expect(summary.kind == .direct)
        })
    }

    @Test("addMembers sends member_uuids (cross-SDK contract)")
    func addMembersBody() async throws {
        try await withServer({ request in
            #expect(request.path == "/api/v1/conversations/01AB/members")
            let body = try request.jsonBody()
            #expect(body["member_uuids"] as? [String] == ["uuid-carol"])
            return StubResponse(json: envelope([
                "conversation_uuid": "01AB", "type": "group", "title": nil, "members": [],
            ]))
        }, { _, client in
            let details = try await client.addMembers("01AB", memberUUIDs: ["uuid-carol"])
            #expect(details.conversationUuid == "01AB")
            #expect(details.type == .group)
        })
    }

    @Test("listMessages builds the after_sequence/limit query")
    func listMessagesQuery() async throws {
        try await withServer({ request in
            #expect(request.target == "/api/v1/conversations/01AB/messages?after_sequence=42&limit=10")
            return StubResponse(json: envelope([
                [
                    "id": "01J", "conversation_id": "01AB",
                    "sender": ["uuid": "u1", "username": "alice", "nickname": nil, "avatar_url": nil],
                    "content": "hi", "content_type": "text/markdown",
                    "created_at": "t", "sequence": 43,
                ],
            ]))
        }, { _, client in
            let messages = try await client.listMessages("01AB", afterSequence: 42, limit: 10)
            #expect(messages.count == 1)
            #expect(messages[0].id == "01J")
            #expect(messages[0].conversationId == "01AB")
            #expect(messages[0].sender.username == "alice")
            #expect(messages[0].contentType == .markdown)
            #expect(messages[0].sequence == 43)
        })
    }

    @Test("eachMessage pages until a short page")
    func eachMessagePagination() async throws {
        let requests = Capture<String>()
        let collected = Capture<Int>()
        try await withServer({ request in
            requests.append(request.target)
            if request.query["after_sequence"] == "0" {
                return StubResponse(json: envelope([
                    message(sequence: 1), message(sequence: 2),
                ]))
            }
            return StubResponse(json: envelope([message(sequence: 3)]))
        }, { _, client in
            try await client.eachMessage("01AB", afterSequence: 0, pageSize: 2) { message in
                collected.append(message.sequence)
            }
            #expect(requests.count == 2)
            #expect(collected.all == [1, 2, 3])
        })
    }

    @Test("sendMessage generates an idempotency key and retries network failures with it")
    func sendMessageIdempotentRetry() async throws {
        let keys = Capture<String>()
        let server = try StubHTTPServer(handler: { request in
            keys.append(request.headers["idempotency-key"] ?? "")
            return StubResponse(json: envelope([
                "id": "01J", "conversation_id": "01AB",
                "sender": ["uuid": "u1", "username": "alice", "nickname": nil, "avatar_url": nil],
                "content": "hi", "content_type": "text/markdown",
                "created_at": "t", "sequence": 1,
            ]))
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(baseURL: server.baseURL, credential: "cred-1")

        // First attempt is dropped mid-connection; the retry must reuse the
        // same Idempotency-Key, so the backend can never see a duplicate.
        server.setDrops(1)
        let message = try await client.sendMessage("01AB", "hi", retries: 1)
        #expect(message.id == "01J")
        #expect(keys.count == 1)
        #expect(!keys.all[0].isEmpty)

        // A second, independent send generates a fresh key.
        server.setDrops(0)
        _ = try await client.sendMessage("01AB", "again")
        #expect(keys.count == 2)
        #expect(keys.all[1] != keys.all[0])
    }

    @Test("auth errors trigger one renewal and retry with the fresh credential")
    func credentialRenewal() async throws {
        let authHeaders = Capture<String>()
        let server = try StubHTTPServer(handler: { request in
            authHeaders.append(request.headers["authorization"] ?? "")
            if authHeaders.count == 1 {
                return StubResponse(json: envelope(nil, code: 10001, message: "expired"))
                    .with(status: 401)
            }
            return StubResponse(json: envelope(["uuid": "u1", "username": "a", "nickname": nil, "avatar_url": nil, "email": nil, "last_seen_at": nil, "created_at": "t", "updated_at": "t"]))
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(
            baseURL: server.baseURL,
            credential: "stale",
            getCredential: { "fresh" })
        let user = try await client.me()
        #expect(user.uuid == "u1")
        #expect(authHeaders.all == ["Bearer stale", "Bearer fresh"])
        let current = await client.currentCredential()
        #expect(current == "fresh")
    }

    @Test("an identical refreshed credential stops the renewal loop")
    func renewalSameCredentialThrows() async throws {
        let calls = Capture<Bool>()
        let server = try StubHTTPServer(handler: { _ in
            StubResponse(json: envelope(nil, code: 10001, message: "expired")).with(status: 401)
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(
            baseURL: server.baseURL,
            credential: "stale",
            getCredential: {
                calls.append(true)
                return "stale"
            })
        do {
            _ = try await client.me()
            Issue.record("expected an error")
        } catch let error as IMError {
            #expect(error.isAuthError)
        }
        #expect(calls.count == 1)
    }

    @Test("uploadFile posts multipart and parses the raw JSON result")
    func uploadFile() async throws {
        let bodies = Capture<String>()
        let server = try StubHTTPServer(handler: { request in
            bodies.append(String(decoding: request.body, as: UTF8.self))
            #expect(request.headers["content-type"]?.hasPrefix("multipart/form-data; boundary=") == true)
            #expect(request.headers["authorization"] == nil)
            return StubResponse(json: ["key": "avatars/1.png", "url": "http://x/1.png", "size": 3])
        })
        server.start()
        defer { server.stop() }

        let client = RESTClient(baseURL: server.baseURL, credential: "cred-1")
        let result = try await client.uploadFile(
            .data(Data("abc".utf8), filename: "1.png"), dir: "avatars")
        #expect(result.key == "avatars/1.png")
        #expect(result.size == 3)
        let body = bodies.last ?? ""
        #expect(body.contains("Content-Disposition: form-data; name=\"file\"; filename=\"1.png\""))
        #expect(body.contains("Content-Type: image/png"))
        #expect(body.contains("name=\"dir\"\r\n\r\navatars"))
    }

    @Test("storageURL escapes segments and keeps slashes")
    func storageURL() async throws {
        try await withServer({ _ in StubResponse(json: envelope(nil)) }, { server, client in
            let url = await client.storageURL("avatars/2026 09/a+b.png")
            #expect(url.absoluteString == "\(server.baseURL)/api/v1/storage/avatars/2026%2009/a%2Bb.png")
        })
    }

    @Test("URLSessionTransport maps connection failures to status 0")
    func transportFailureStatusZero() async throws {
        // Port 1 is reserved and nothing listens there; the request must
        // fail as a transport error.
        let client = RESTClient(baseURL: "http://127.0.0.1:1", credential: "c")
        do {
            _ = try await client.me()
            Issue.record("expected a transport failure")
        } catch let error as IMError {
            #expect(error.status == 0)
            #expect(error.code == -1)
        }
    }

    private func message(sequence: Int) -> [String: Any] {
        [
            "id": "01J\(sequence)", "conversation_id": "01AB",
            "sender": ["uuid": "u1", "username": "alice", "nickname": nil, "avatar_url": nil],
            "content": "m\(sequence)", "content_type": "text/markdown",
            "created_at": "t", "sequence": sequence,
        ]
    }
}

extension StubHTTPServer.Response {
    func with(status: Int) -> StubHTTPServer.Response {
        var copy = self
        copy.status = status
        return copy
    }
}
