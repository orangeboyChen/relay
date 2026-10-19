import XCTest
@testable import SimDeck

@MainActor
final class WebRTCCallMediaSessionTests: XCTestCase {
    func testRelayGatheringBecomesReadyOnFirstRelayCandidate() {
        var gate = ICEGatheringGate(requiresRelayCandidate: true)

        let host = gate.observe(
            candidateSDP: "candidate:1 1 udp 2122260223 192.168.1.10 50000 typ host"
        )
        let relay = gate.observe(
            candidateSDP: "candidate:2 1 udp 1677734911 115.45.41.0 55021 typ relay raddr 192.168.31.140 rport 55021"
        )

        XCTAssertFalse(host.isUsable)
        XCTAssertTrue(relay.isUsable)
        XCTAssertTrue(gate.hasUsableCandidate)
        XCTAssertEqual(gate.observedCandidateCount, 2)
        XCTAssertEqual(gate.usableCandidateCount, 1)
        XCTAssertEqual(relay.transport, "udp")
        XCTAssertEqual(relay.type, "relay")
    }

    func testRelayDescriptionRejectsHostOnlyCandidates() {
        let gate = ICEGatheringGate(requiresRelayCandidate: true)
        let hostOnly = """
        v=0\r
        m=audio 9 UDP/TLS/RTP/SAVPF 0\r
        a=candidate:1 1 udp 2122260223 192.168.1.10 50000 typ host\r
        """
        let relayed = hostOnly +
            "a=candidate:2 1 tcp 1677734911 115.45.41.0 55002 typ relay tcptype passive\r\n"

        XCTAssertFalse(gate.containsUsableCandidate(in: hostOnly))
        XCTAssertTrue(gate.containsUsableCandidate(in: relayed))
    }

    func testDirectGatheringAcceptsHostCandidate() {
        var gate = ICEGatheringGate(requiresRelayCandidate: false)

        let candidate = gate.observe(
            candidateSDP: "candidate:1 1 udp 2122260223 192.168.1.10 50000 typ host"
        )

        XCTAssertTrue(candidate.isUsable)
        XCTAssertTrue(gate.hasUsableCandidate)
    }

    func testOfferContainsCompleteAudioOnlyPCMUDescription() async throws {
        let session = try WebRTCCallMediaSession(offerTimeout: .seconds(5))
        defer { session.close() }

        let offer = try await session.makeOffer()

        XCTAssertTrue(offer.contains("m=audio"))
        XCTAssertTrue(offer.localizedCaseInsensitiveContains("PCMU/8000"))
        XCTAssertTrue(offer.contains("a=sendrecv"))
        XCTAssertTrue(offer.contains("a=candidate:"))
        XCTAssertFalse(offer.contains("m=video"))
    }

    func testRejectsAnswerWithoutPCMU() async throws {
        let session = try WebRTCCallMediaSession(offerTimeout: .seconds(5))
        defer { session.close() }

        do {
            try await session.applyAnswer(
                "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n"
            )
            XCTFail("Expected the incompatible answer to fail")
        } catch {
            XCTAssertEqual(error as? CallMediaSessionError, .invalidAnswer)
        }
    }
}
