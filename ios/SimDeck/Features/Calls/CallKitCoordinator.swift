import AVFAudio
import CallKit
import Foundation
import Observation

@MainActor
@Observable
final class CallKitCoordinator: NSObject, CallStarting, CallControlling {
    private(set) var activeCall: ActiveCallSession?
    private(set) var isSpeakerEnabled = false

    let capabilities: CallCapabilities

    @ObservationIgnored private let provider: CXProvider
    @ObservationIgnored private let controller: CXCallController
    @ObservationIgnored private let transport: any CallTransport
    @ObservationIgnored private var pendingOutgoing: [UUID: OutgoingCallRequest] = [:]
    @ObservationIgnored private var startCompletions: [UUID: (Result<Void, Error>) -> Void] = [:]

    init(
        transport: any CallTransport = UnavailableCallTransport(),
        provider: CXProvider? = nil,
        controller: CXCallController = CXCallController(),
        initialCall: ActiveCallSession? = nil
    ) {
        activeCall = initialCall
        self.transport = transport
        self.controller = controller
        capabilities = transport.capabilities

        if let provider {
            self.provider = provider
        } else {
            let configuration = CXProviderConfiguration(localizedName: "sim-deck")
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

    func endCall() async throws {
        guard let activeCall else { return }
        try await controller.request(CXTransaction(action: CXEndCallAction(call: activeCall.id)))
    }

    func setHeld(_ isHeld: Bool) async throws {
        guard let activeCall, capabilities.supportsHolding else { return }
        try await controller.request(
            CXTransaction(action: CXSetHeldCallAction(call: activeCall.id, onHold: isHeld))
        )
    }

    func setMuted(_ isMuted: Bool) async throws {
        guard let activeCall, capabilities.supportsMuting else { return }
        try await controller.request(
            CXTransaction(action: CXSetMutedCallAction(call: activeCall.id, muted: isMuted))
        )
    }

    func playDTMF(_ digits: String) async throws {
        guard let activeCall, capabilities.supportsDTMF else { return }
        try await controller.request(
            CXTransaction(
                action: CXPlayDTMFCallAction(call: activeCall.id, digits: digits, type: .singleTone)
            )
        )
    }

    func setSpeakerEnabled(_ isEnabled: Bool) throws {
        try AVAudioSession.sharedInstance().overrideOutputAudioPort(isEnabled ? .speaker : .none)
        isSpeakerEnabled = isEnabled
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
            provider.reportOutgoingCall(with: callID, connectedAt: date)
        case .ended(let callID, let reason):
            guard activeCall?.id == callID else { return }
            provider.reportCall(with: callID, endedAt: .now, reason: reason.callKitReason)
            finishCall(callID: callID)
        }
    }

    private func configureAudioSession() throws {
        try AVAudioSession.sharedInstance().setCategory(
            .playAndRecord,
            mode: .voiceChat,
            options: [.allowBluetoothHFP]
        )
    }

    private func completeStart(callID: UUID, result: Result<Void, Error>) {
        let completion = startCompletions.removeValue(forKey: callID)
        pendingOutgoing.removeValue(forKey: callID)
        completion?(result)
    }

    private func failStart(callID: UUID, error: Error) {
        completeStart(callID: callID, result: .failure(error))
        if activeCall?.id == callID {
            activeCall = nil
        }
    }

    private func finishCall(callID: UUID) {
        guard activeCall?.id == callID else { return }
        activeCall = nil
        isSpeakerEnabled = false
        pendingOutgoing.removeValue(forKey: callID)
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

        do {
            try configureAudioSession()
            activeCall?.status = .connecting
            provider.reportOutgoingCall(with: request.callID, startedConnectingAt: .now)
        } catch {
            action.fail()
            failStart(callID: request.callID, error: error)
            return
        }

        Task {
            do {
                try await transport.startOutgoingCall(
                    callID: request.callID,
                    number: request.number,
                    lineID: request.lineID
                )
                action.fulfill()
                completeStart(callID: request.callID, result: .success(()))
            } catch {
                action.fail()
                provider.reportCall(with: request.callID, endedAt: .now, reason: .failed)
                failStart(callID: request.callID, error: error)
            }
        }
    }

    func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
        guard activeCall?.id == action.callUUID else {
            action.fail()
            return
        }
        let previousStatus = activeCall?.status
        activeCall?.status = .ending

        Task {
            do {
                try await transport.endCall(callID: action.callUUID)
                action.fulfill()
                finishCall(callID: action.callUUID)
            } catch {
                action.fail()
                if let previousStatus {
                    activeCall?.status = previousStatus
                }
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
