import AVFAudio
import CallKit
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
        let recentLineStore = RecordingRecentCallLineStore()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            recentCallLineStore: recentLineStore
        )
        let lineID = UUID()

        try await coordinator.startCall(to: "10086", using: lineID)

        let callID = try XCTUnwrap(coordinator.activeCall?.id)
        XCTAssertEqual(transport.startedCalls, [.init(callID: callID, number: "10086", lineID: lineID)])
        XCTAssertEqual(
            recentLineStore.routes,
            [.init(callID: callID, remoteNumber: "10086", lineID: lineID)]
        )
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

    func testEndFailureDoesNotRestoreTheVisibleCall() async {
        let transport = RecordingCallTransport(endError: TestCallError.failed)
        let callID = UUID()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            initialCall: ActiveCallSession(
                id: callID,
                lineID: UUID(),
                remoteNumber: "10086",
                displayName: nil,
                status: .active(connectedAt: Date(timeIntervalSince1970: 1_000)),
                isMuted: false
            )
        )

        do {
            try await coordinator.endCall()
            XCTFail("Expected endCall to report the transport error")
        } catch {
            XCTAssertEqual(error as? TestCallError, .failed)
        }

        XCTAssertNil(coordinator.activeCall)
    }

    func testInProcessStartFailureKeepsCallVisibleUntilFailureIsDismissed() async {
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

        XCTAssertEqual(coordinator.activeCall?.status, .failed)
        XCTAssertEqual(coordinator.callStartFailureID, coordinator.activeCall?.id)

        coordinator.dismissCallStartFailure()

        XCTAssertNil(coordinator.activeCall)
        XCTAssertNil(coordinator.callStartFailureID)
    }

    func testReportsIncomingCallWithRemoteNumberAndSIMIdentity() async throws {
        let provider = RecordingSystemCallProvider()
        let transport = RecordingCallTransport()
        let recentLineStore = RecordingRecentCallLineStore()
        let coordinator = CallKitCoordinator(
            transport: transport,
            provider: provider,
            integratesWithSystem: false,
            recentCallLineStore: recentLineStore
        )
        let call = makeIncomingCall(contactName: "Home")

        try await coordinator.reportIncomingCall(call)

        XCTAssertEqual(provider.incomingCallID, call.callID)
        XCTAssertEqual(provider.incomingUpdate?.remoteHandle?.value, call.remoteNumber)
        XCTAssertEqual(provider.incomingUpdate?.localizedCallerName, "Home · SIM 2")
        XCTAssertEqual(provider.incomingUpdate?.hasVideo, false)
        XCTAssertEqual(provider.incomingUpdate?.supportsHolding, true)
        XCTAssertEqual(provider.incomingUpdate?.supportsDTMF, true)
        XCTAssertEqual(coordinator.activeCall?.status, .ringing)
        XCTAssertEqual(coordinator.activeCall?.lineID, call.lineID)
        XCTAssertEqual(transport.registeredIncomingCalls.first?.callSecret, call.callSecret)
        XCTAssertEqual(
            recentLineStore.routes,
            [
                .init(
                    callID: call.callID,
                    remoteNumber: call.remoteNumber,
                    lineID: call.lineID
                )
            ]
        )
    }

    func testIncomingCallReportFailureClearsVisibleCall() async {
        let provider = RecordingSystemCallProvider(reportError: TestCallError.failed)
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            provider: provider,
            integratesWithSystem: false
        )
        let call = makeIncomingCall(contactName: nil)

        do {
            try await coordinator.reportIncomingCall(call)
            XCTFail("Expected reportIncomingCall to throw")
        } catch {
            XCTAssertEqual(error as? TestCallError, .failed)
        }

        XCTAssertNil(coordinator.activeCall)
        XCTAssertEqual(transport.discardedIncomingCallIDs, [call.callID])
    }

    func testIncomingAnswerClaimsBeforeStartingMedia() async throws {
        let provider = RecordingSystemCallProvider()
        let transport = RecordingCallTransport()
        let claimer = RecordingIncomingCallClaimer(won: true)
        let coordinator = CallKitCoordinator(
            transport: transport,
            incomingCallClaimer: claimer,
            provider: provider,
            integratesWithSystem: false
        )
        let call = makeIncomingCall(contactName: nil)
        try await coordinator.reportIncomingCall(call)

        let answered = try await coordinator.answerIncomingCall(callID: call.callID)

        XCTAssertTrue(answered)
        XCTAssertEqual(claimer.claims, [.init(call.notificationID, call.installationID)])
        XCTAssertEqual(transport.answeredIncomingCallIDs, [call.callID])
        XCTAssertEqual(coordinator.activeCall?.status, .connecting)
    }

    func testIncomingAnswerEndsAsAnsweredElsewhereWhenClaimLoses() async throws {
        let provider = RecordingSystemCallProvider()
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            incomingCallClaimer: RecordingIncomingCallClaimer(won: false),
            provider: provider,
            integratesWithSystem: false
        )
        let call = makeIncomingCall(contactName: nil)
        try await coordinator.reportIncomingCall(call)

        let answered = try await coordinator.answerIncomingCall(callID: call.callID)

        XCTAssertFalse(answered)
        XCTAssertEqual(provider.endedReason, .answeredElsewhere)
        XCTAssertEqual(transport.discardedIncomingCallIDs, [call.callID])
        XCTAssertTrue(transport.answeredIncomingCallIDs.isEmpty)
        XCTAssertNil(coordinator.activeCall)
    }

    func testRemoteClaimEndsTheMatchingIncomingCallAsAnsweredElsewhere() async throws {
        let provider = RecordingSystemCallProvider()
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            provider: provider,
            integratesWithSystem: false
        )
        let call = makeIncomingCall(contactName: nil)
        try await coordinator.reportIncomingCall(call)

        coordinator.reportIncomingCallAnsweredElsewhere(notificationID: call.notificationID)

        XCTAssertEqual(provider.endedCallID, call.callID)
        XCTAssertEqual(provider.endedReason, .answeredElsewhere)
        XCTAssertEqual(transport.discardedIncomingCallIDs, [call.callID])
        XCTAssertNil(coordinator.activeCall)
    }

    func testRemoteClaimForAnotherNotificationLeavesIncomingCallRinging() async throws {
        let provider = RecordingSystemCallProvider()
        let coordinator = CallKitCoordinator(
            transport: RecordingCallTransport(),
            provider: provider,
            integratesWithSystem: false
        )
        let call = makeIncomingCall(contactName: nil)
        try await coordinator.reportIncomingCall(call)

        coordinator.reportIncomingCallAnsweredElsewhere(notificationID: UUID())

        XCTAssertNil(provider.endedReason)
        XCTAssertEqual(coordinator.activeCall?.status, .ringing)
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

    private func makeIncomingCall(contactName: String?) -> IncomingCallPresentation {
        IncomingCallPresentation(
            notificationID: UUID(),
            callID: UUID(),
            lineID: UUID(),
            installationID: UUID(),
            numericID: 2,
            remoteNumber: "+8613500000000",
            contactName: contactName,
            callSecret: Data(repeating: 1, count: 32),
            deviceVerificationKey: Data(repeating: 2, count: 32)
        )
    }
}

private final class RecordingSystemCallProvider: SystemCallProviding {
    private let reportError: Error?
    private(set) var incomingCallID: UUID?
    private(set) var incomingUpdate: CXCallUpdate?
    private(set) var endedCallID: UUID?
    private(set) var endedReason: CXCallEndedReason?

    init(reportError: Error? = nil) {
        self.reportError = reportError
    }

    func setDelegate(_ delegate: (any CXProviderDelegate)?, queue: DispatchQueue?) {}

    func reportNewIncomingCall(
        with UUID: UUID,
        update: CXCallUpdate,
        completion: @escaping @Sendable (Error?) -> Void
    ) {
        incomingCallID = UUID
        incomingUpdate = update
        completion(reportError)
    }

    func reportCall(with UUID: UUID, updated update: CXCallUpdate) {}
    func reportCall(with UUID: UUID, endedAt dateEnded: Date?, reason endedReason: CXCallEndedReason) {
        endedCallID = UUID
        self.endedReason = endedReason
    }
    func reportOutgoingCall(with UUID: UUID, startedConnectingAt dateStartedConnecting: Date?) {}
    func reportOutgoingCall(with UUID: UUID, connectedAt dateConnected: Date?) {}
}

@MainActor
private final class RecordingRecentCallLineStore: RecentCallLineStoring {
    struct Route: Equatable {
        let callID: UUID
        let remoteNumber: String
        let lineID: UUID
    }

    private(set) var routes: [Route] = []

    func record(callID: UUID, remoteNumber: String, lineID: UUID) {
        routes.append(Route(callID: callID, remoteNumber: remoteNumber, lineID: lineID))
    }

    func lineID(callRecordIdentifier: String?, remoteNumber: String) -> UUID? {
        nil
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
    private let endError: Error?
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?

    private(set) var startedCalls: [StartedCall] = []
    private(set) var endedCallIDs: [UUID] = []
    private(set) var holdUpdates: [BooleanUpdate] = []
    private(set) var muteUpdates: [BooleanUpdate] = []
    private(set) var dtmfUpdates: [DTMFUpdate] = []
    private(set) var registeredIncomingCalls: [IncomingCallRegistration] = []
    private(set) var discardedIncomingCallIDs: [UUID] = []
    private(set) var answeredIncomingCallIDs: [UUID] = []

    init(startError: Error? = nil, endError: Error? = nil) {
        self.startError = startError
        self.endError = endError
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {
        registeredIncomingCalls.append(call)
    }

    func discardIncomingCall(callID: UUID) {
        discardedIncomingCallIDs.append(callID)
    }

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        if let startError {
            throw startError
        }
        startedCalls.append(.init(callID: callID, number: number, lineID: lineID))
        eventHandler?(.ringing(callID: callID))
        eventHandler?(.connected(callID: callID, at: connectedAt))
    }

    func answerIncomingCall(callID: UUID) async throws {
        answeredIncomingCallIDs.append(callID)
    }
    func rejectIncomingCall(callID: UUID) async throws {}

    func endCall(callID: UUID) async throws {
        if let endError {
            throw endError
        }
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

@MainActor
private final class RecordingIncomingCallClaimer: IncomingCallClaiming {
    struct Claim: Equatable {
        let notificationID: UUID
        let installationID: UUID

        init(_ notificationID: UUID, _ installationID: UUID) {
            self.notificationID = notificationID
            self.installationID = installationID
        }
    }

    private let won: Bool
    private(set) var claims: [Claim] = []

    init(won: Bool) {
        self.won = won
    }

    func claimIncomingCall(
        notificationID: UUID,
        installationID: UUID
    ) async throws -> IncomingCallClaim {
        claims.append(.init(notificationID, installationID))
        return IncomingCallClaim(
            notificationID: notificationID,
            installationID: installationID,
            claimedAt: Date(timeIntervalSince1970: 1_000),
            won: won
        )
    }
}

private enum TestCallError: Error, Equatable {
    case failed
}
