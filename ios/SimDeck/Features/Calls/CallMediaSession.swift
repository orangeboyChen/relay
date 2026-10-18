import AVFAudio
import Foundation
import WebRTC

enum CallMediaSessionError: LocalizedError, Equatable, Sendable {
    case peerCreationFailed
    case audioTrackCreationFailed
    case pcmuUnavailable
    case offerCreationFailed
    case iceGatheringTimedOut
    case invalidAnswer
    case closed

    var errorDescription: String? {
        switch self {
        case .peerCreationFailed, .audioTrackCreationFailed:
            String(localized: "The secure audio connection could not be created.")
        case .pcmuUnavailable:
            String(localized: "The required call audio codec is unavailable.")
        case .offerCreationFailed, .iceGatheringTimedOut:
            String(localized: "The secure audio connection could not be prepared.")
        case .invalidAnswer:
            String(localized: "The device returned an invalid audio response.")
        case .closed:
            String(localized: "The secure audio connection is closed.")
        }
    }
}

@MainActor
protocol CallMediaSession: AnyObject {
    func makeOffer() async throws -> String
    func applyAnswer(_ answer: String) async throws
    func setMuted(_ isMuted: Bool)
    func audioSessionDidActivate(_ audioSession: AVAudioSession)
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession)
    func close()
}

@MainActor
final class WebRTCCallMediaSession: NSObject, CallMediaSession {
    private static let sharedFactory: RTCPeerConnectionFactory = {
        RTCInitializeSSL()
        return RTCPeerConnectionFactory()
    }()

    private let peerConnection: RTCPeerConnection
    private let localAudioTrack: RTCAudioTrack
    private let offerTimeout: Duration
    private var iceContinuation: CheckedContinuation<String, Error>?
    private var iceTimeoutTask: Task<Void, Never>?
    private var isClosed = false

    init(
        factory: RTCPeerConnectionFactory = WebRTCCallMediaSession.sharedFactory,
        iceServers: [CallTURNCredentials] = [],
        offerTimeout: Duration = .seconds(10)
    ) throws {
        self.offerTimeout = offerTimeout

        let configuration = RTCConfiguration()
        configuration.sdpSemantics = .unifiedPlan
        configuration.iceServers = iceServers.map { server in
            RTCIceServer(
                urlStrings: server.urls,
                username: server.username,
                credential: server.credential
            )
        }
        if !iceServers.isEmpty {
            configuration.iceTransportPolicy = .relay
        }
        configuration.tcpCandidatePolicy = .enabled
        configuration.continualGatheringPolicy = .gatherOnce

        let peerConstraints = RTCMediaConstraints(
            mandatoryConstraints: nil,
            optionalConstraints: ["DtlsSrtpKeyAgreement": "true"]
        )
        guard let peerConnection = factory.peerConnection(
            with: configuration,
            constraints: peerConstraints,
            delegate: nil
        ) else {
            throw CallMediaSessionError.peerCreationFailed
        }

        let audioSource = factory.audioSource(with: nil)
        let localAudioTrack = factory.audioTrack(
            with: audioSource,
            trackId: "sim-deck-audio"
        )
        guard peerConnection.add(localAudioTrack, streamIds: ["sim-deck"]) != nil,
              let transceiver = peerConnection.transceivers.first(where: {
                  $0.mediaType == .audio
              }) else {
            peerConnection.close()
            throw CallMediaSessionError.audioTrackCreationFailed
        }

        let pcmuCodecs = factory
            .rtpSenderCapabilities(forKind: kRTCMediaStreamTrackKindAudio)
            .codecs
            .filter {
                $0.name.caseInsensitiveCompare("PCMU") == .orderedSame
                    && $0.clockRate?.intValue == 8_000
                    && ($0.numChannels?.intValue ?? 1) == 1
            }
        guard !pcmuCodecs.isEmpty else {
            peerConnection.close()
            throw CallMediaSessionError.pcmuUnavailable
        }
        do {
            try transceiver.setCodecPreferences(pcmuCodecs, error: ())
        } catch {
            peerConnection.close()
            throw CallMediaSessionError.pcmuUnavailable
        }

        self.peerConnection = peerConnection
        self.localAudioTrack = localAudioTrack
        super.init()
        peerConnection.delegate = self

        let audioSession = RTCAudioSession.sharedInstance()
        audioSession.useManualAudio = true
        audioSession.isAudioEnabled = false
    }

    func makeOffer() async throws -> String {
        guard !isClosed else { throw CallMediaSessionError.closed }

        let constraints = RTCMediaConstraints(
            mandatoryConstraints: [
                "OfferToReceiveAudio": "true",
                "OfferToReceiveVideo": "false",
            ],
            optionalConstraints: nil
        )
        let offerSDP: String = try await withCheckedThrowingContinuation {
            continuation in
            peerConnection.offer(for: constraints) { description, error in
                if let description {
                    continuation.resume(returning: description.sdp)
                } else {
                    continuation.resume(throwing: CallMediaSessionError.offerCreationFailed)
                }
            }
        }
        try await setLocalDescription(
            RTCSessionDescription(type: .offer, sdp: offerSDP)
        )

        if peerConnection.iceGatheringState == .complete {
            return try completeLocalDescription()
        }
        return try await withCheckedThrowingContinuation { continuation in
            iceContinuation = continuation
            iceTimeoutTask = Task { [weak self] in
                guard let self else { return }
                do {
                    try await Task.sleep(for: offerTimeout)
                } catch {
                    return
                }
                failPendingOffer(with: .iceGatheringTimedOut)
            }
        }
    }

    func applyAnswer(_ answer: String) async throws {
        guard !isClosed else { throw CallMediaSessionError.closed }
        guard answer.contains("m=audio"),
              answer.localizedCaseInsensitiveContains("PCMU/8000") else {
            throw CallMediaSessionError.invalidAnswer
        }
        let description = RTCSessionDescription(type: .answer, sdp: answer)
        do {
            try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<Void, Error>) in
                peerConnection.setRemoteDescription(description) { error in
                    if error != nil {
                        continuation.resume(throwing: CallMediaSessionError.invalidAnswer)
                    } else {
                        continuation.resume(returning: ())
                    }
                }
            }
        } catch {
            throw CallMediaSessionError.invalidAnswer
        }
    }

    func setMuted(_ isMuted: Bool) {
        localAudioTrack.isEnabled = !isMuted
    }

    func audioSessionDidActivate(_ audioSession: AVAudioSession) {
        let rtcAudioSession = RTCAudioSession.sharedInstance()
        rtcAudioSession.audioSessionDidActivate(audioSession)
        rtcAudioSession.isAudioEnabled = true
    }

    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) {
        let rtcAudioSession = RTCAudioSession.sharedInstance()
        rtcAudioSession.isAudioEnabled = false
        rtcAudioSession.audioSessionDidDeactivate(audioSession)
    }

    func close() {
        guard !isClosed else { return }
        isClosed = true
        localAudioTrack.isEnabled = false
        failPendingOffer(with: .closed)
        peerConnection.close()
    }

    private func setLocalDescription(_ description: RTCSessionDescription) async throws {
        do {
            try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<Void, Error>) in
                peerConnection.setLocalDescription(description) { error in
                    if error != nil {
                        continuation.resume(throwing: CallMediaSessionError.offerCreationFailed)
                    } else {
                        continuation.resume(returning: ())
                    }
                }
            }
        } catch {
            throw CallMediaSessionError.offerCreationFailed
        }
    }

    private func completeLocalDescription() throws -> String {
        guard let description = peerConnection.localDescription?.sdp,
              description.contains("m=audio"),
              description.localizedCaseInsensitiveContains("PCMU/8000"),
              !description.contains("m=video") else {
            throw CallMediaSessionError.offerCreationFailed
        }
        return description
    }

    private func finishPendingOffer() {
        guard let continuation = iceContinuation else { return }
        iceContinuation = nil
        iceTimeoutTask?.cancel()
        iceTimeoutTask = nil
        do {
            continuation.resume(returning: try completeLocalDescription())
        } catch {
            continuation.resume(throwing: error)
        }
    }

    private func failPendingOffer(with error: CallMediaSessionError) {
        guard let continuation = iceContinuation else { return }
        iceContinuation = nil
        iceTimeoutTask?.cancel()
        iceTimeoutTask = nil
        continuation.resume(throwing: error)
    }
}

extension WebRTCCallMediaSession: RTCPeerConnectionDelegate {
    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didChange stateChanged: RTCSignalingState
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didAdd stream: RTCMediaStream
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didRemove stream: RTCMediaStream
    ) {}

    nonisolated func peerConnectionShouldNegotiate(_ peerConnection: RTCPeerConnection) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didChange newState: RTCIceConnectionState
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didChange newState: RTCIceGatheringState
    ) {
        guard newState == .complete else { return }
        Task { @MainActor [weak self] in
            self?.finishPendingOffer()
        }
    }

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didGenerate candidate: RTCIceCandidate
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didRemove candidates: [RTCIceCandidate]
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didOpen dataChannel: RTCDataChannel
    ) {}
}
