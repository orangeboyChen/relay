import XCTest
@testable import SimDeck

@MainActor
final class CallLifecycleTests: XCTestCase {
    func testTransportEventsAdvanceTheVisibleCallLifecycle() {
        let callID = UUID()
        let connectedAt = Date(timeIntervalSince1970: 1_000)
        let coordinator = makeCoordinator(callID: callID)

        coordinator.handleTransportEvent(.ringing(callID: callID))
        XCTAssertEqual(coordinator.activeCall?.status, .ringing)

        coordinator.handleTransportEvent(.connected(callID: callID, at: connectedAt))
        XCTAssertEqual(coordinator.activeCall?.status, .active(connectedAt: connectedAt))
    }

    func testTransportEventsForAnotherCallDoNotMutateTheVisibleCall() {
        let coordinator = makeCoordinator(callID: UUID())

        coordinator.handleTransportEvent(.ringing(callID: UUID()))

        XCTAssertEqual(coordinator.activeCall?.status, .requesting)
    }

    func testActiveAndHeldStatusesPreserveConnectionTime() {
        let connectedAt = Date(timeIntervalSince1970: 2_000)

        XCTAssertEqual(ActiveCallStatus.active(connectedAt: connectedAt).connectedAt, connectedAt)
        XCTAssertEqual(ActiveCallStatus.held(connectedAt: connectedAt).connectedAt, connectedAt)
        XCTAssertNil(ActiveCallStatus.connecting.connectedAt)
    }

    private func makeCoordinator(callID: UUID) -> CallKitCoordinator {
        let call = ActiveCallSession(
            id: callID,
            lineID: UUID(),
            remoteNumber: "10086",
            displayName: nil,
            status: .requesting,
            isMuted: false
        )
        return CallKitCoordinator(
            transport: UnavailableCallTransport(),
            initialCall: call
        )
    }
}
