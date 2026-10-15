import AVFAudio
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

    func testInProcessCallFlowStartsControlsAndEndsWithoutSystemTransactions() async throws {
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false
        )
        let lineID = UUID()

        try await coordinator.startCall(to: "10086", using: lineID)

        let callID = try XCTUnwrap(coordinator.activeCall?.id)
        XCTAssertEqual(transport.startedCalls, [.init(callID: callID, number: "10086", lineID: lineID)])
        XCTAssertEqual(coordinator.activeCall?.status, .active(connectedAt: transport.connectedAt))

        try await coordinator.setMuted(true)
        try await coordinator.setHeld(true)
        try await coordinator.playDTMF("8")

        XCTAssertEqual(transport.muteUpdates, [.init(callID: callID, value: true)])
        XCTAssertEqual(transport.holdUpdates, [.init(callID: callID, value: true)])
        XCTAssertEqual(transport.dtmfUpdates, [.init(callID: callID, digits: "8")])
        XCTAssertTrue(coordinator.activeCall?.isMuted == true)
        XCTAssertEqual(coordinator.activeCall?.status, .held(connectedAt: transport.connectedAt))

        try await coordinator.endCall()

        XCTAssertEqual(transport.endedCallIDs, [callID])
        XCTAssertNil(coordinator.activeCall)
    }

    func testInProcessStartFailureClearsCallAndPropagatesError() async {
        let transport = RecordingCallTransport(startError: TestCallError.failed)
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false
        )

        do {
            try await coordinator.startCall(to: "10086", using: UUID())
            XCTFail("Expected startCall to throw")
        } catch {
            XCTAssertEqual(error as? TestCallError, .failed)
        }

        XCTAssertNil(coordinator.activeCall)
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

@MainActor
private final class RecordingCallTransport: CallTransport {
    struct StartedCall: Equatable {
        let callID: UUID
        let number: String
        let lineID: UUID
    }

    struct BooleanUpdate: Equatable {
        let callID: UUID
        let value: Bool
    }

    struct DTMFUpdate: Equatable {
        let callID: UUID
        let digits: String
    }

    let capabilities = CallCapabilities.cellular
    let connectedAt = Date(timeIntervalSince1970: 3_000)
    private let startError: Error?
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?

    private(set) var startedCalls: [StartedCall] = []
    private(set) var endedCallIDs: [UUID] = []
    private(set) var holdUpdates: [BooleanUpdate] = []
    private(set) var muteUpdates: [BooleanUpdate] = []
    private(set) var dtmfUpdates: [DTMFUpdate] = []

    init(startError: Error? = nil) {
        self.startError = startError
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        if let startError {
            throw startError
        }
        startedCalls.append(.init(callID: callID, number: number, lineID: lineID))
        eventHandler?(.ringing(callID: callID))
        eventHandler?(.connected(callID: callID, at: connectedAt))
    }

    func endCall(callID: UUID) async throws {
        endedCallIDs.append(callID)
        eventHandler?(.ended(callID: callID, reason: .remoteEnded))
    }

    func setHeld(_ isHeld: Bool, callID: UUID) async throws {
        holdUpdates.append(.init(callID: callID, value: isHeld))
    }

    func setMuted(_ isMuted: Bool, callID: UUID) async throws {
        muteUpdates.append(.init(callID: callID, value: isMuted))
    }

    func playDTMF(_ digits: String, callID: UUID) async throws {
        dtmfUpdates.append(.init(callID: callID, digits: digits))
    }

    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async {}
}

private enum TestCallError: Error, Equatable {
    case failed
}
