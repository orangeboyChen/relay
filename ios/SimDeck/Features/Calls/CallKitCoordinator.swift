import AVFAudio
import CallKit
import Foundation
import Observation

@MainActor
@Observable
final class CallKitCoordinator: NSObject, CallStarting, CallControlling, IncomingCallReporting {
    private(set) var activeCall: ActiveCallSession?
    private(set) var callStartFailureID: UUID?

    let capabilities: CallCapabilities
    let audioRoutes: CallAudioRouteController

    @ObservationIgnored private let provider: any SystemCallProviding
    @ObservationIgnored private let controller: CXCallController
    @ObservationIgnored private let transport: any CallTransport
    @ObservationIgnored private let incomingCallClaimer: (any IncomingCallClaiming)?
    @ObservationIgnored private let recentCallLineStore: any RecentCallLineStoring
    @ObservationIgnored private let integratesWithSystem: Bool
    @ObservationIgnored private var pendingOutgoing: [UUID: OutgoingCallRequest] = [:]
    @ObservationIgnored private var incomingCalls: [UUID: IncomingCallPresentation] = [:]
    @ObservationIgnored private var startCompletions: [UUID: (Result<Void, Error>) -> Void] = [:]
    @ObservationIgnored private var pendingEndCallKinds: [UUID: Bool] = [:]
    @ObservationIgnored private var pendingOutgoingEndReasons: [UUID: CallEndReason] = [:]

    init(
        transport: any CallTransport = UnavailableCallTransport(),
        incomingCallClaimer: (any IncomingCallClaiming)? = nil,
        provider: (any SystemCallProviding)? = nil,
        controller: CXCallController = CXCallController(),
        integratesWithSystem: Bool = true,
        initialCall: ActiveCallSession? = nil,
        recentCallLineStore: any RecentCallLineStoring = NullRecentCallLineStore(),
        audioRoutes: CallAudioRouteController? = nil
    ) {
        activeCall = initialCall
        self.audioRoutes = audioRoutes ?? CallAudioRouteController()
        self.transport = transport
        self.incomingCallClaimer = incomingCallClaimer
        self.controller = controller
        self.integratesWithSystem = integratesWithSystem
        self.recentCallLineStore = recentCallLineStore
        capabilities = transport.capabilities

        if let provider {
            self.provider = provider
        } else {
            let configuration = CXProviderConfiguration()
            configuration.supportsVideo = false
            configuration.maximumCallsPerCallGroup = 2
            configuration.maximumCallGroups = 1
            configuration.supportedHandleTypes = [.phoneNumber]
            self.provider = CXProvider(configuration: configuration)
        }

        super.init()
        self.provider.setDelegate(self, queue: .main)
        transport.setEventHandler { [weak self] event in
            self?.handleTransportEvent(event)
        }
    }

    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {
        (transport as? any CallLineRouting)?.updateLineRoutes(lines)
    }

    func startCall(to number: String, using lineID: UUID) async throws {
        let callID = UUID()
        let request = OutgoingCallRequest(callID: callID, number: number, lineID: lineID)
        let action = CXStartCallAction(
            call: callID,
            handle: CXHandle(type: .phoneNumber, value: number)
        )
        action.isVideo = false

        activeCall = ActiveCallSession(
            id: callID,
            lineID: lineID,
            remoteNumber: number,
            displayName: nil,
            status: .requesting,
            isMuted: false
        )
        pendingOutgoing[callID] = request

        guard integratesWithSystem else {
            try await startOutgoingCall(request, reportToCallKit: false)
            return
        }

        try await withCheckedThrowingContinuation { continuation in
            startCompletions[callID] = { result in
                continuation.resume(with: result)
            }

            Task {
                do {
                    try await controller.request(CXTransaction(action: action))
                } catch {
                    failStart(callID: callID, error: error)
                }
            }
        }
    }

    func reportIncomingCall(_ call: IncomingCallPresentation) async throws {
        let update = incomingCallUpdate(for: call)
        try transport.registerIncomingCall(
            IncomingCallRegistration(
                callID: call.callID,
                lineID: call.lineID,
                callSecret: call.callSecret,
                verificationKey: call.deviceVerificationKey
            )
        )
        activeCall = ActiveCallSession(
            id: call.callID,
            lineID: call.lineID,
            remoteNumber: call.remoteNumber,
            displayName: call.contactName,
            status: .ringing,
            isMuted: false
        )
        incomingCalls[call.callID] = call

        do {
            try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<Void, any Error>) in
                provider.reportNewIncomingCall(with: call.callID, update: update) { error in
                    if let error {
                        continuation.resume(throwing: error)
                    } else {
                        continuation.resume()
                    }
                }
            }
            recentCallLineStore.record(
                callID: call.callID,
                remoteNumber: call.remoteNumber,
                lineID: call.lineID
            )
        } catch {
            transport.discardIncomingCall(callID: call.callID)
            finishCall(callID: call.callID)
            throw error
        }
    }

    @discardableResult
    func answerIncomingCall(callID: UUID) async throws -> Bool {
        guard let call = incomingCalls[callID], let incomingCallClaimer else {
            throw CallLifecycleError.incomingCallUnavailable
        }
        let claim = try await incomingCallClaimer.claimIncomingCall(
            notificationID: call.notificationID,
            installationID: call.installationID
        )
        guard claim.won else {
            provider.reportCall(with: callID, endedAt: .now, reason: .answeredElsewhere)
            transport.discardIncomingCall(callID: callID)
            finishCall(callID: callID)
            return false
        }

        activeCall?.status = .connecting
        do {
            if integratesWithSystem {
                try configureAudioSession()
            }
            try await transport.answerIncomingCall(callID: callID)
            incomingCalls.removeValue(forKey: callID)
            return true
        } catch {
            activeCall?.status = .ringing
            throw error
        }
    }

    func updateIncomingCallName(_ name: String?, callID: UUID) {
        guard let activeCall, activeCall.id == callID,
              let incomingCall = incomingCalls[callID] else { return }
        self.activeCall?.displayName = name
        let update = CXCallUpdate()
        update.remoteHandle = CXHandle(type: .phoneNumber, value: activeCall.remoteNumber)
        let caller = name.flatMap { $0.isEmpty ? nil : $0 } ?? activeCall.remoteNumber
        update.localizedCallerName = "\(caller) · SIM \(incomingCall.numericID)"
        provider.reportCall(with: callID, updated: update)
    }

    func reportIncomingCallAnsweredElsewhere(notificationID: UUID) {
        guard let match = incomingCalls.first(where: { $0.value.notificationID == notificationID }) else {
            return
        }
        let callID = match.key
        provider.reportCall(with: callID, endedAt: .now, reason: .answeredElsewhere)
        transport.discardIncomingCall(callID: callID)
        incomingCalls.removeValue(forKey: callID)
        finishCall(callID: callID)
    }

    func endCall() async throws {
        guard let activeCall else { return }
        let callID = activeCall.id
        let isIncoming = incomingCalls[callID] != nil
        finishCall(callID: callID)

        if integratesWithSystem {
            pendingEndCallKinds[callID] = isIncoming
            do {
                try await controller.request(CXTransaction(action: CXEndCallAction(call: callID)))
            } catch {
                pendingEndCallKinds.removeValue(forKey: callID)
                throw error
            }
            return
        }

        if isIncoming {
            try await transport.rejectIncomingCall(callID: callID)
            return
        }
        try await transport.endCall(callID: callID)
    }

    func setHeld(_ isHeld: Bool) async throws {
        guard let activeCall, capabilities.supportsHolding else { return }
        guard integratesWithSystem else {
            try await transport.setHeld(isHeld, callID: activeCall.id)
            if let connectedAt = activeCall.status.connectedAt {
                self.activeCall?.status = isHeld
                    ? .held(connectedAt: connectedAt)
                    : .active(connectedAt: connectedAt)
            }
            return
        }
        try await controller.request(
            CXTransaction(action: CXSetHeldCallAction(call: activeCall.id, onHold: isHeld))
        )
    }

    func setMuted(_ isMuted: Bool) async throws {
        guard let activeCall, capabilities.supportsMuting else { return }
        guard integratesWithSystem else {
            try await transport.setMuted(isMuted, callID: activeCall.id)
            self.activeCall?.isMuted = isMuted
            return
        }
        try await controller.request(
            CXTransaction(action: CXSetMutedCallAction(call: activeCall.id, muted: isMuted))
        )
    }

    func playDTMF(_ digits: String) async throws {
        guard let activeCall, capabilities.supportsDTMF else { return }
        guard integratesWithSystem else {
            try await transport.playDTMF(digits, callID: activeCall.id)
            return
        }
        try await controller.request(
            CXTransaction(
                action: CXPlayDTMFCallAction(call: activeCall.id, digits: digits, type: .singleTone)
            )
        )
    }

    func updateDisplayName(_ displayName: String?, for callID: UUID) {
        guard activeCall?.id == callID else { return }
        activeCall?.displayName = displayName
    }

    func handleTransportEvent(_ event: CallTransportEvent) {
        switch event {
        case .ringing(let callID):
            guard activeCall?.id == callID else { return }
            activeCall?.status = .ringing
        case .connected(let callID, let date):
            guard activeCall?.id == callID else { return }
            activeCall?.status = .active(connectedAt: date)
            if integratesWithSystem {
                provider.reportOutgoingCall(with: callID, connectedAt: date)
            }
        case .ended(let callID, let reason):
            guard activeCall?.id == callID else { return }
            if pendingOutgoing[callID] != nil {
                pendingOutgoingEndReasons[callID] = reason
                return
            }
            finishEndedCall(callID: callID, reason: reason)
        }
    }

    private func finishEndedCall(callID: UUID, reason: CallEndReason) {
        guard activeCall?.id == callID else { return }
        if integratesWithSystem {
            provider.reportCall(with: callID, endedAt: .now, reason: reason.callKitReason)
        }
        finishCall(callID: callID)
    }

    private func configureAudioSession() throws {
        try AVAudioSession.sharedInstance().setCategory(
            .playAndRecord,
            mode: .voiceChat,
            options: [.allowBluetoothHFP]
        )
        audioRoutes.refresh()
    }

    private func incomingCallUpdate(for call: IncomingCallPresentation) -> CXCallUpdate {
        let update = CXCallUpdate()
        update.remoteHandle = CXHandle(type: .phoneNumber, value: call.remoteNumber)
        update.localizedCallerName = call.callerName
        update.hasVideo = false
        update.supportsHolding = capabilities.supportsHolding
        update.supportsDTMF = capabilities.supportsDTMF
        update.supportsGrouping = false
        update.supportsUngrouping = false
        return update
    }

    private func startOutgoingCall(
        _ request: OutgoingCallRequest,
        reportToCallKit: Bool
    ) async throws {
        do {
            if reportToCallKit {
                try configureAudioSession()
            }
            activeCall?.status = .connecting
            if reportToCallKit {
                provider.reportOutgoingCall(with: request.callID, startedConnectingAt: .now)
            }
            try await transport.startOutgoingCall(
                callID: request.callID,
                number: request.number,
                lineID: request.lineID
            )
            recentCallLineStore.record(
                callID: request.callID,
                remoteNumber: request.number,
                lineID: request.lineID
            )
            completeStart(callID: request.callID, result: .success(()))
        } catch {
            if reportToCallKit {
                provider.reportCall(with: request.callID, endedAt: .now, reason: .failed)
            }
            failStart(callID: request.callID, error: error)
            throw error
        }
    }

    private func completeStart(callID: UUID, result: Result<Void, Error>) {
        let pendingEndReason = pendingOutgoingEndReasons.removeValue(forKey: callID)
        let completion = startCompletions.removeValue(forKey: callID)
        pendingOutgoing.removeValue(forKey: callID)
        incomingCalls.removeValue(forKey: callID)
        completion?(result)
        if case .success = result, let pendingEndReason {
            finishEndedCall(callID: callID, reason: pendingEndReason)
        }
    }

    private func failStart(callID: UUID, error: Error) {
        completeStart(callID: callID, result: .failure(error))
        if activeCall?.id == callID {
            activeCall?.status = .failed
            callStartFailureID = callID
        }
    }

    func dismissCallStartFailure() {
        guard let callStartFailureID else { return }
        finishCall(callID: callStartFailureID)
    }

    func completeSystemEndCall(callID: UUID, isIncoming: Bool, fulfill: () -> Void) {
        if activeCall?.id == callID {
            finishCall(callID: callID)
        }

        // The local hangup is complete before the remote modem command finishes.
        // Fulfilling CallKit first prevents a slow or offline device from showing
        // a system-level "Call Control Failed" error after the user hung up.
        fulfill()
        Task {
            do {
                if isIncoming {
                    try await transport.rejectIncomingCall(callID: callID)
                } else {
                    try await transport.endCall(callID: callID)
                }
            } catch {
#if DEBUG
                print("call end command failed after local hangup: \(String(reflecting: error))")
#endif
            }
            pendingEndCallKinds.removeValue(forKey: callID)
        }
    }

    private func finishCall(callID: UUID) {
        guard activeCall?.id == callID else { return }
        activeCall = nil
        if callStartFailureID == callID {
            callStartFailureID = nil
        }
        pendingOutgoing.removeValue(forKey: callID)
        pendingOutgoingEndReasons.removeValue(forKey: callID)
        incomingCalls.removeValue(forKey: callID)
        if let completion = startCompletions.removeValue(forKey: callID) {
            completion(.failure(CallLifecycleError.endedBeforeStart))
        }
    }
}

extension CallKitCoordinator: @preconcurrency CXProviderDelegate {
    func providerDidReset(_ provider: CXProvider) {
        if let callID = activeCall?.id {
            finishCall(callID: callID)
        }
        Task {
            await transport.reset()
        }
    }

    func provider(_ provider: CXProvider, perform action: CXStartCallAction) {
        guard let request = pendingOutgoing[action.callUUID] else {
            action.fail()
            return
        }

        Task {
            do {
                try await startOutgoingCall(request, reportToCallKit: true)
                action.fulfill()
            } catch {
                action.fail()
            }
        }
    }

    func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
        let callID = action.callUUID
        guard activeCall?.id == callID || pendingEndCallKinds[callID] != nil else {
            action.fail()
            return
        }
        let isIncoming = pendingEndCallKinds[callID] ?? (incomingCalls[callID] != nil)
        completeSystemEndCall(callID: callID, isIncoming: isIncoming) {
            action.fulfill()
        }
    }

    func provider(_ provider: CXProvider, perform action: CXAnswerCallAction) {
        Task {
            do {
                if try await answerIncomingCall(callID: action.callUUID) {
                    action.fulfill()
                } else {
                    action.fail()
                }
            } catch {
                action.fail()
                self.provider.reportCall(
                    with: action.callUUID,
                    endedAt: .now,
                    reason: .failed
                )
                transport.discardIncomingCall(callID: action.callUUID)
                finishCall(callID: action.callUUID)
            }
        }
    }

    func provider(_ provider: CXProvider, perform action: CXSetHeldCallAction) {
        Task {
            do {
                try await transport.setHeld(action.isOnHold, callID: action.callUUID)
                if let connectedAt = activeCall?.status.connectedAt {
                    activeCall?.status = action.isOnHold
                        ? .held(connectedAt: connectedAt)
                        : .active(connectedAt: connectedAt)
                }
                action.fulfill()
            } catch {
                action.fail()
            }
        }
    }

    func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
        Task {
            do {
                try await transport.setMuted(action.isMuted, callID: action.callUUID)
                activeCall?.isMuted = action.isMuted
                action.fulfill()
            } catch {
                action.fail()
            }
        }
    }

    func provider(_ provider: CXProvider, perform action: CXPlayDTMFCallAction) {
        Task {
            do {
                try await transport.playDTMF(action.digits, callID: action.callUUID)
                action.fulfill()
            } catch {
                action.fail()
            }
        }
    }

    func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {
        audioRoutes.refresh()
        Task {
            await transport.audioSessionDidActivate(audioSession)
        }
    }

    func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {
        Task {
            await transport.audioSessionDidDeactivate(audioSession)
        }
    }
}

private struct OutgoingCallRequest {
    let callID: UUID
    let number: String
    let lineID: UUID
}

enum CallLifecycleError: Error {
    case endedBeforeStart
    case incomingCallUnavailable
}

private extension CallEndReason {
    var callKitReason: CXCallEndedReason {
        switch self {
        case .remoteEnded:
            .remoteEnded
        case .failed:
            .failed
        case .unanswered:
            .unanswered
        }
    }
}
