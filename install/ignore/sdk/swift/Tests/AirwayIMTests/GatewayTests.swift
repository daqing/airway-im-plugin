import Testing
import Foundation
@testable import AirwayIM

@Suite("GatewaySocket")
struct GatewayTests {
    private final class Harness: @unchecked Sendable {
        let fakes = Capture<FakeWebSocketTransport>()
        let events = Capture<GatewayEvent>()
        let statuses = Capture<ConnectionStatus>()
        let errors = Capture<IMError>()
        let readyCount = LockedBox(0)
        let refreshedCount = LockedBox(0)
        /// Applied to each fake the moment it is created — before the
        /// gateway can send its auth frame.
        let script = LockedBox<(@Sendable (FakeWebSocketTransport) -> Void)?>(nil)

        func makeGateway(
            credential: String = "cred-1",
            getCredential: CredentialProvider? = nil
        ) -> GatewaySocket {
            GatewaySocket(
                wsURL: "ws://gateway.test",
                credential: credential,
                pingInterval: 0, // keep timing out of most tests
                getCredential: getCredential,
                socketFactory: { [harness = self] _ in
                    let fake = FakeWebSocketTransport.make()
                    if let apply = harness.script.current { apply(fake) }
                    harness.fakes.append(fake)
                    return fake
                },
                callbacks: GatewayCallbacks(
                    onEvent: { [harness = self] in harness.events.append($0) },
                    onStatus: { [harness = self] in harness.statuses.append($0) },
                    onReady: { [harness = self] in harness.readyCount.set(harness.readyCount.current + 1) },
                    onError: { [harness = self] in harness.errors.append($0) }))
        }
    }

    @Test("the first application frame is the auth command; success goes online")
    func authHandshake() async throws {
        let harness = Harness()
        harness.script.set { $0.answerAuthOK() }
        let gateway = harness.makeGateway()
        await gateway.connect()

        let fake = try await waitForValue("first connection") { harness.fakes.all.first }
        try await waitFor("auth frame sent") { !fake.sentTexts.all.isEmpty }
        let frame = try JSONSerialization.jsonObject(with: Data(fake.sentTexts.all[0].utf8)) as? [String: Any]
        #expect(frame?["cmd"] as? String == "auth")
        #expect(frame?["opts"] as? [String] == ["cred-1"])

        try await waitFor("online") { await gateway.isOnline }
        #expect(harness.statuses.all.contains(.authenticating))
        #expect(harness.statuses.all.contains(.online))
    }

    @Test("event frames are delivered once; duplicates dropped by event_id")
    func eventDedupe() async throws {
        let harness = Harness()
        harness.script.set { $0.answerAuthOK() }
        let gateway = harness.makeGateway()
        await gateway.connect()
        let fake = try await waitForValue("first connection") { harness.fakes.all.first }
        try await waitFor("online") { await gateway.isOnline }

        await fake.serverEnqueue(eventFrame(eventId: "e1", conversationId: "01AB", sequence: 2))
        await fake.serverEnqueue(eventFrame(eventId: "e1", conversationId: "01AB", sequence: 2))
        await fake.serverEnqueue(#"{"cmd":"ping","code":0,"data":"PONG"}"#) // command replies carry no event

        try await waitFor("event delivered") { harness.events.count == 1 }
        try await Task.sleep(for: .milliseconds(100))
        #expect(harness.events.count == 1)
        #expect(harness.events.all[0].eventId == "e1")
    }

    @Test("a dropped socket reconnects with backoff and re-authenticates")
    func reconnectAfterDrop() async throws {
        let harness = Harness()
        harness.script.set { $0.answerAuthOK() }
        let gateway = harness.makeGateway()
        await gateway.connect()
        let first = try await waitForValue("first connection") { harness.fakes.all.first }
        try await waitFor("online") { await gateway.isOnline }

        await first.serverClose()
        try await waitFor("reconnecting") { harness.statuses.all.last == .reconnecting }

        let second = try await waitForValue("second connection") {
            harness.fakes.count >= 2 ? harness.fakes.all[1] : nil
        }
        await second.answerAuthOK()
        try await waitFor("online again") { await gateway.isOnline }
        let secondFrame = try JSONSerialization.jsonObject(with: Data(second.sentTexts.all[0].utf8)) as? [String: Any]
        #expect(secondFrame?["cmd"] as? String == "auth")
        #expect(harness.readyCount.current >= 2)
    }

    @Test("an auth rejection with getCredential refreshes once and recovers")
    func authRejectionRefreshesCredential() async throws {
        let harness = Harness()
        harness.script.set { [harness = harness] fake in
            if harness.fakes.count == 0 {
                fake.answerAuthRejected() // first connection: credential rejected
            } else {
                fake.answerAuthOK() // recovered with the fresh credential
            }
        }
        let gateway = harness.makeGateway(
            credential: "stale",
            getCredential: { [harness = harness] in
                harness.refreshedCount.set(harness.refreshedCount.current + 1)
                return "fresh"
            })
        await gateway.connect()
        let first = try await waitForValue("first connection") { harness.fakes.all.first }
        try await waitFor("error reported") { !harness.errors.all.isEmpty }
        #expect(harness.errors.all[0].message.contains("gateway auth failed"))

        let second = try await waitForValue("second connection") {
            harness.fakes.count >= 2 ? harness.fakes.all[1] : nil
        }
        await second.answerAuthOK()
        try await waitFor("online with fresh credential") { await gateway.isOnline }
        let freshFrame = try JSONSerialization.jsonObject(with: Data(second.sentTexts.all[0].utf8)) as? [String: Any]
        #expect(freshFrame?["opts"] as? [String] == ["fresh"])
        #expect(harness.refreshedCount.current == 1)
    }

    @Test("a repeated rejection with the same credential stops the loop offline")
    func repeatedRejectionStopsOffline() async throws {
        let harness = Harness()
        harness.script.set { $0.answerAuthRejected() }
        let gateway = harness.makeGateway(credential: "stale") // no getCredential
        await gateway.connect()

        try await waitFor("offline") { harness.statuses.all.last == .offline }
        try await Task.sleep(for: .milliseconds(700))
        #expect(harness.fakes.count == 1) // no reconnect loop
    }

    @Test("close() stops everything and reports closed")
    func closeStops() async throws {
        let harness = Harness()
        harness.script.set { $0.answerAuthOK() }
        let gateway = harness.makeGateway()
        await gateway.connect()
        try await waitFor("online") { await gateway.isOnline }

        await gateway.close()
        #expect(harness.statuses.all.last == .closed)
        try await Task.sleep(for: .milliseconds(100))
        let onlineAfterClose = await gateway.isOnline
        #expect(!onlineAfterClose)
    }
}
