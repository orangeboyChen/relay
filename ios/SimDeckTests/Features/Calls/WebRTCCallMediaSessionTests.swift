import XCTest
@testable import SimDeck

@MainActor
final class WebRTCCallMediaSessionTests: XCTestCase {
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
