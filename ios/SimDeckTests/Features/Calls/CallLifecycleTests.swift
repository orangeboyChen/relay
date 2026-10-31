// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

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
        XCTAssertTrue(coordinator.showsInAppCallUI)

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
        XCTAssertFalse(coordinator.showsInAppCallUI)
    }

    func testCompletedOutgoingCallIsAddedToAppHistory() async throws {
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 3_125) }
        )
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Test modem",
            lineName: "Primary",
            phoneNumber: "13500000000",
            isOnline: true
        )
        let model = AppModel(
            devices: [line],
            callCoordinator: coordinator,
            remoteDeviceRefreshEnabled: false
        )

        try await coordinator.startCall(to: "10086", using: line.id)
        try await coordinator.endCall()

        guard case .loaded(let records) = model.callHistory else {
            return XCTFail("Expected loaded call history")
        }
        let record = try XCTUnwrap(records.first)
        XCTAssertEqual(record.lineID, line.id)
        XCTAssertEqual(record.lineSnapshot, LineHistorySnapshot(line: line))
        XCTAssertEqual(record.remoteNumber, "10086")
        XCTAssertEqual(record.direction, .outgoing)
        XCTAssertEqual(record.result, .answered)
        XCTAssertEqual(record.startedAt, transport.connectedAt)
        XCTAssertEqual(record.endedAt, Date(timeIntervalSince1970: 3_125))
        XCTAssertEqual(record.duration, 125)
    }

    func testCompletedCallIsPersistedAndRestoredAfterBootstrap() async throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let callHistoryStore = EncryptedCallHistoryStore(
            fileURL: directory.appending(path: "calls.bin"),
            now: { Date(timeIntervalSince1970: 10_000) }
        )
        let messageHistoryStore = EncryptedMessageHistoryStore(
            fileURL: directory.appending(path: "messages.bin")
        )
        let vaultKey = VaultKey.random()
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 10_000) }
        )
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Test modem",
            lineName: "Primary",
            phoneNumber: "13500000000",
            isOnline: true
        )
        let model = AppModel(
            devices: [line],
            callCoordinator: coordinator,
            installationBootstrapper: CallHistoryInstallationBootstrapper(vaultKey: vaultKey),
            messageHistoryStore: messageHistoryStore,
            callHistoryStore: callHistoryStore,
            remoteDeviceRefreshEnabled: false
        )
        model.bootstrapInstallation()

        try await coordinator.startCall(to: "10086", using: line.id)
        try await coordinator.endCall()

        let restored = try callHistoryStore.load(using: vaultKey)
        XCTAssertEqual(restored.count, 1)
        XCTAssertEqual(restored[0].lineSnapshot, LineHistorySnapshot(line: line))
        XCTAssertEqual(restored[0].remoteNumber, "10086")
        XCTAssertEqual(restored[0].result, .answered)
    }

    func testDeletingSelectedCallHistoryUpdatesMemoryAndEncryptedStorage() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let callHistoryStore = EncryptedCallHistoryStore(
            fileURL: directory.appending(path: "calls.bin"),
            now: { Date(timeIntervalSince1970: 10_000) }
        )
        let vaultKey = VaultKey.random()
        let first = CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "13800000000",
            contactName: nil,
            direction: .outgoing,
            result: .answered,
            startedAt: Date(timeIntervalSince1970: 9_000),
            endedAt: Date(timeIntervalSince1970: 9_100)
        )
        let second = CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "13500000000",
            contactName: nil,
            direction: .incoming,
            result: .missed,
            startedAt: Date(timeIntervalSince1970: 9_500),
            endedAt: Date(timeIntervalSince1970: 9_500)
        )
        try callHistoryStore.save([first, second], using: vaultKey)
        let model = AppModel(
            installationBootstrapper: CallHistoryInstallationBootstrapper(vaultKey: vaultKey),
            callHistoryStore: callHistoryStore,
            remoteDeviceRefreshEnabled: false
        )
        model.bootstrapInstallation()

        try model.deleteCallHistoryRecords(withIDs: [first.id])

        XCTAssertEqual(model.callHistory, .loaded([second]))
        XCTAssertEqual(try callHistoryStore.load(using: vaultKey), [second])
    }

    func testUnansweredIncomingCallIsRecordedAsMissed() async throws {
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 4_010) }
        )
        var records: [CallRecord] = []
        coordinator.setHistoryHandler { records.append($0) }
        let call = makeIncomingCall(contactName: "Home")

        try await coordinator.reportIncomingCall(call)
        coordinator.handleTransportEvent(.ended(callID: call.callID, reason: .unanswered))

        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records[0].direction, .incoming)
        XCTAssertEqual(records[0].result, .missed)
        XCTAssertEqual(records[0].contactName, "Home")
        XCTAssertEqual(records[0].startedAt, Date(timeIntervalSince1970: 4_010))
        XCTAssertEqual(records[0].endedAt, Date(timeIntervalSince1970: 4_010))
    }

    func testDeclinedIncomingCallIsRecordedAsRejected() async throws {
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 5_000) }
        )
        var records: [CallRecord] = []
        coordinator.setHistoryHandler { records.append($0) }
        let call = makeIncomingCall(contactName: nil)

        try await coordinator.reportIncomingCall(call)
        try await coordinator.endCall()

        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records[0].direction, .incoming)
        XCTAssertEqual(records[0].result, .rejected)
    }

    func testAnsweredIncomingCallIsRecordedOnceAsAnswered() async throws {
        let transport = RecordingCallTransport()
        let coordinator = CallKitCoordinator(
            transport: transport,
            incomingCallClaimer: RecordingIncomingCallClaimer(won: true),
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 5_125) }
        )
        var records: [CallRecord] = []
        coordinator.setHistoryHandler { records.append($0) }
        let call = makeIncomingCall(contactName: "Home")

        try await coordinator.reportIncomingCall(call)
        let answered = try await coordinator.answerIncomingCall(callID: call.callID)
        XCTAssertTrue(answered)
        coordinator.handleTransportEvent(
            .connected(callID: call.callID, at: Date(timeIntervalSince1970: 5_000))
        )
        coordinator.handleTransportEvent(.ended(callID: call.callID, reason: .remoteEnded))
        coordinator.handleTransportEvent(.ended(callID: call.callID, reason: .remoteEnded))

        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records[0].direction, .incoming)
        XCTAssertEqual(records[0].result, .answered)
        XCTAssertEqual(records[0].startedAt, Date(timeIntervalSince1970: 5_000))
        XCTAssertEqual(records[0].endedAt, Date(timeIntervalSince1970: 5_125))
    }

    func testFailedOutgoingCallIsRecordedAfterFailureIsDismissed() async {
        let coordinator = CallKitCoordinator(
            transport: RecordingCallTransport(startError: TestCallError.failed),
            integratesWithSystem: false,
            now: { Date(timeIntervalSince1970: 6_000) }
        )
        var records: [CallRecord] = []
        coordinator.setHistoryHandler { records.append($0) }

        do {
            try await coordinator.startCall(to: "10086", using: UUID())
            XCTFail("Expected startCall to throw")
        } catch {
            XCTAssertEqual(error as? TestCallError, .failed)
        }
        XCTAssertTrue(records.isEmpty)

        coordinator.dismissCallStartFailure()
        coordinator.dismissCallStartFailure()

        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records[0].direction, .outgoing)
        XCTAssertEqual(records[0].result, .failed)
        XCTAssertEqual(records[0].startedAt, Date(timeIntervalSince1970: 6_000))
        XCTAssertEqual(records[0].endedAt, Date(timeIntervalSince1970: 6_000))
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

    func testSystemEndCallCompletesBeforeRemoteCleanupFinishes() {
        let transport = RecordingCallTransport(endError: TestCallError.failed)
        let callID = UUID()
        let coordinator = CallKitCoordinator(
            transport: transport,
            provider: RecordingSystemCallProvider(),
            integratesWithSystem: true,
            initialCall: ActiveCallSession(
                id: callID,
                lineID: UUID(),
                remoteNumber: "13500000000",
                displayName: nil,
                status: .connecting,
                isMuted: false
            )
        )
        var didFulfill = false

        coordinator.completeSystemEndCall(callID: callID, isIncoming: false) {
            didFulfill = true
        }

        XCTAssertTrue(didFulfill)
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

    func testSystemStartActionIsFulfilledBeforeRemoteSetupCompletes() async throws {
        let transport = SuspendedStartCallTransport()
        let controller = RecordingSystemCallController()
        let coordinator = CallKitCoordinator(
            transport: transport,
            provider: RecordingSystemCallProvider(),
            controller: controller,
            integratesWithSystem: true
        )
        let startTask = Task {
            try await coordinator.startCall(to: "13500000000", using: UUID())
        }
        for _ in 0..<10 where controller.transactions.isEmpty {
            await Task.yield()
        }
        let transaction = try XCTUnwrap(controller.transactions.first)
        let action = try XCTUnwrap(transaction.actions.first as? CXStartCallAction)
        var didFulfill = false
        var didFail = false

        coordinator.beginSystemStartCall(callID: action.callUUID) {
            didFulfill = true
        } fail: {
            didFail = true
        }

        XCTAssertTrue(didFulfill)
        XCTAssertFalse(didFail)
        XCTAssertEqual(coordinator.activeCall?.status, .ringing)
        XCTAssertFalse(coordinator.showsInAppCallUI)
        for _ in 0..<10 where !transport.isStartPending {
            await Task.yield()
        }
        XCTAssertTrue(transport.isStartPending)
        transport.completeStart()
        try await startTask.value
    }

    func testEndEventDuringOutgoingStartIsAppliedAfterStartCompletes() async throws {
        let transport = RecordingCallTransport(endsBeforeStartCompletes: true)
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false
        )

        try await coordinator.startCall(to: "10086", using: UUID())

        XCTAssertEqual(transport.startedCalls.count, 1)
        XCTAssertNil(coordinator.activeCall)
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
        XCTAssertFalse(coordinator.showsInAppCallUI)
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

    func testProviderResetClearsTheVisibleCallAndResetsTransportResources() async {
        let transport = RecordingCallTransport()
        let callID = UUID()
        let coordinator = CallKitCoordinator(
            transport: transport,
            integratesWithSystem: false,
            initialCall: ActiveCallSession(
                id: callID,
                lineID: UUID(),
                remoteNumber: "10086",
                displayName: nil,
                status: .active(connectedAt: .now),
                isMuted: false
            )
        )

        coordinator.providerDidReset(CXProvider(configuration: CXProviderConfiguration()))
        for _ in 0..<100 where transport.resetCount == 0 {
            await Task.yield()
        }

        XCTAssertNil(coordinator.activeCall)
        XCTAssertEqual(transport.resetCount, 1)
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

private struct CallHistoryInstallationBootstrapper: InstallationBootstrapping {
    let vaultKey: VaultKey

    func bootstrap() throws -> InstallationBootstrapResult {
        InstallationBootstrapResult(
            identity: InstallationIdentity(
                id: UUID(),
                signingPrivateKey: Data(repeating: 1, count: 32),
                keyAgreementPrivateKey: Data(repeating: 2, count: 32),
                pendingRevocationIDs: []
            ),
            recoveredVaultKey: vaultKey,
            pendingRevocationIDs: []
        )
    }

    func saveVaultKey(_ key: VaultKey) throws {}
    func markInstallationRevoked(_ id: UUID) throws {}
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
private final class RecordingSystemCallController: SystemCallControlling {
    private(set) var transactions: [CXTransaction] = []

    func request(_ transaction: CXTransaction) async throws {
        transactions.append(transaction)
    }
}

@MainActor
private final class SuspendedStartCallTransport: CallTransport {
    let capabilities = CallCapabilities.cellular
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?
    private var startContinuation: CheckedContinuation<Void, Never>?

    var isStartPending: Bool { startContinuation != nil }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {}
    func discardIncomingCall(callID: UUID) {}

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        await withCheckedContinuation { continuation in
            startContinuation = continuation
        }
        eventHandler?(.ringing(callID: callID))
    }

    func completeStart() {
        startContinuation?.resume()
        startContinuation = nil
    }

    func answerIncomingCall(callID: UUID) async throws {}
    func rejectIncomingCall(callID: UUID) async throws {}
    func endCall(callID: UUID) async throws {}
    func addParticipant(number: String, callID: UUID) async throws {}
    func mergeCalls(callID: UUID) async throws {}
    func setHeld(_ isHeld: Bool, callID: UUID) async throws {}
    func setMuted(_ isMuted: Bool, callID: UUID) async throws {}
    func playDTMF(_ digits: String, callID: UUID) async throws {}
    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async {}
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
    private let endsBeforeStartCompletes: Bool
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?

    private(set) var startedCalls: [StartedCall] = []
    private(set) var endedCallIDs: [UUID] = []
    private(set) var holdUpdates: [BooleanUpdate] = []
    private(set) var muteUpdates: [BooleanUpdate] = []
    private(set) var dtmfUpdates: [DTMFUpdate] = []
    private(set) var registeredIncomingCalls: [IncomingCallRegistration] = []
    private(set) var discardedIncomingCallIDs: [UUID] = []
    private(set) var answeredIncomingCallIDs: [UUID] = []
    private(set) var resetCount = 0

    init(
        startError: Error? = nil,
        endError: Error? = nil,
        endsBeforeStartCompletes: Bool = false
    ) {
        self.startError = startError
        self.endError = endError
        self.endsBeforeStartCompletes = endsBeforeStartCompletes
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
        if endsBeforeStartCompletes {
            eventHandler?(.ended(callID: callID, reason: .remoteEnded))
        }
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

    func addParticipant(number: String, callID: UUID) async throws {}
    func mergeCalls(callID: UUID) async throws {}

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
    func reset() async {
        resetCount += 1
    }
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
