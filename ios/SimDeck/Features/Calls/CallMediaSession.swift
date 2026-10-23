// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import AVFAudio
import Foundation
import OSLog
import WebRTC

enum CallMediaSessionError: LocalizedError, Equatable, Sendable {
    case peerCreationFailed
    case audioTrackCreationFailed
    case pcmuUnavailable
    case offerCreationFailed
    case iceGatheringTimedOut
    case connectivityTimedOut
    case invalidAnswer
    case closed

    var errorDescription: String? {
        switch self {
        case .peerCreationFailed, .audioTrackCreationFailed:
            String(localized: "The secure audio connection could not be created.")
        case .pcmuUnavailable:
            String(localized: "The required call audio codec is unavailable.")
        case .offerCreationFailed, .iceGatheringTimedOut, .connectivityTimedOut:
            String(localized: "The secure audio connection could not be prepared.")
        case .invalidAnswer:
            String(localized: "The device returned an invalid audio response.")
        case .closed:
            String(localized: "The secure audio connection is closed.")
        }
    }
}

enum CallMediaConnectivityState: Equatable, Sendable {
    case connected
    case disconnected
    case failed
}

struct CallMediaActivity: Equatable, Sendable {
    let inboundPackets: UInt64
    let outboundPackets: UInt64
}

@MainActor
protocol CallMediaSession: AnyObject {
    func makeOffer() async throws -> String
    func makeRestartOffer(iceServers: [CallTURNCredentials]) async throws -> String
    func applyAnswer(_ answer: String) async throws
    func waitUntilConnected(timeout: Duration) async throws
    func mediaActivity() async throws -> CallMediaActivity
    func setConnectivityHandler(_ handler: (@MainActor (CallMediaConnectivityState) -> Void)?)
    func setMuted(_ isMuted: Bool)
    func audioSessionDidActivate(_ audioSession: AVAudioSession)
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession)
    func close()
}

@MainActor
final class WebRTCCallMediaSession: NSObject, CallMediaSession {
    private static let logger = Logger(
        subsystem: "com.nowcent.simdeck",
        category: "CallMediaSession"
    )

    private static let sharedFactory: RTCPeerConnectionFactory = {
        RTCInitializeSSL()
        return RTCPeerConnectionFactory()
    }()

    private let peerConnection: RTCPeerConnection
    private let localAudioTrack: RTCAudioTrack
    private let offerTimeout: Duration
    private var gatheringGate: ICEGatheringGate
    private var iceContinuation: CheckedContinuation<String, Error>?
    private var iceTimeoutTask: Task<Void, Never>?
    private var connectivityHandler: (@MainActor (CallMediaConnectivityState) -> Void)?
    private var connectivityContinuation: CheckedContinuation<Void, Error>?
    private var connectivityTimeoutTask: Task<Void, Never>?
    private var isClosed = false

    init(
        factory: RTCPeerConnectionFactory = WebRTCCallMediaSession.sharedFactory,
        iceServers: [CallTURNCredentials] = [],
        offerTimeout: Duration = .seconds(10)
    ) throws {
        self.offerTimeout = offerTimeout
        gatheringGate = ICEGatheringGate(requiresRelayCandidate: !iceServers.isEmpty)

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
        configuration.continualGatheringPolicy = .gatherContinually

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
        Self.logger.info(
            "WebRTC audio session created; relay required: \(!iceServers.isEmpty, privacy: .public), ICE server count: \(iceServers.count, privacy: .public)"
        )
    }

    func makeOffer() async throws -> String {
        try await makeOffer(restartingICE: false)
    }

    func makeRestartOffer(iceServers: [CallTURNCredentials]) async throws -> String {
        guard !isClosed else { throw CallMediaSessionError.closed }
        guard iceContinuation == nil else { throw CallMediaSessionError.offerCreationFailed }
        let configuration = peerConnection.configuration
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
        guard peerConnection.setConfiguration(configuration) else {
            Self.logger.error("WebRTC configuration refresh failed before ICE restart")
            throw CallMediaSessionError.offerCreationFailed
        }
        peerConnection.restartIce()
        return try await makeOffer(restartingICE: true)
    }

    private func makeOffer(restartingICE: Bool) async throws -> String {
        guard !isClosed else { throw CallMediaSessionError.closed }
        guard iceContinuation == nil else { throw CallMediaSessionError.offerCreationFailed }
        gatheringGate = ICEGatheringGate(
            requiresRelayCandidate: peerConnection.configuration.iceTransportPolicy == .relay
        )
        Self.logger.info("Creating WebRTC audio offer; ICE restart: \(restartingICE, privacy: .public)")

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
        Self.logger.info("WebRTC audio offer created; setting local description")
        try await setLocalDescription(
            RTCSessionDescription(type: .offer, sdp: offerSDP)
        )
        Self.logger.info(
            "WebRTC local description set; ICE state: \(String(describing: self.peerConnection.iceGatheringState), privacy: .public)"
        )

        if gatheringGate.hasUsableCandidate {
            Self.logger.info("WebRTC offer is ready with a usable ICE candidate")
            return try completeLocalDescription()
        }
        return try await withCheckedThrowingContinuation { continuation in
            iceContinuation = continuation
            if gatheringGate.hasUsableCandidate {
                finishPendingOffer()
                return
            }
            iceTimeoutTask = Task { [weak self] in
                guard let self else { return }
                do {
                    try await Task.sleep(for: offerTimeout)
                } catch {
                    return
                }
                Self.logger.error(
                    "WebRTC ICE gathering timed out; state: \(String(describing: self.peerConnection.iceGatheringState), privacy: .public), candidates: \(self.gatheringGate.observedCandidateCount, privacy: .public), usable: \(self.gatheringGate.usableCandidateCount, privacy: .public)"
                )
                failPendingOffer(with: .iceGatheringTimedOut)
            }
        }
    }

    func setConnectivityHandler(
        _ handler: (@MainActor (CallMediaConnectivityState) -> Void)?
    ) {
        connectivityHandler = handler
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

    func waitUntilConnected(timeout: Duration) async throws {
        guard !isClosed else { throw CallMediaSessionError.closed }
        if peerConnection.iceConnectionState == .connected
            || peerConnection.iceConnectionState == .completed {
            return
        }
        guard connectivityContinuation == nil else {
            throw CallMediaSessionError.offerCreationFailed
        }
        try await withCheckedThrowingContinuation { continuation in
            connectivityContinuation = continuation
            connectivityTimeoutTask = Task { [weak self] in
                do {
                    try await Task.sleep(for: timeout)
                } catch {
                    return
                }
                self?.finishConnectivityWait(with: .failure(.connectivityTimedOut))
            }
        }
    }

    func mediaActivity() async throws -> CallMediaActivity {
        guard !isClosed else { throw CallMediaSessionError.closed }
        return await withCheckedContinuation { continuation in
            peerConnection.statistics { statistics in
                var inboundPackets: UInt64 = 0
                var outboundPackets: UInt64 = 0
                for statistic in statistics.statistics.values {
                    guard statistic.type == "inbound-rtp" || statistic.type == "outbound-rtp" else {
                        continue
                    }
                    let kind = (statistic.values["kind"] as? NSString) as String?
                        ?? (statistic.values["mediaType"] as? NSString) as String?
                    guard kind == nil || kind == "audio" else { continue }
                    if statistic.type == "inbound-rtp" {
                        inboundPackets += (statistic.values["packetsReceived"] as? NSNumber)?.uint64Value ?? 0
                    } else {
                        outboundPackets += (statistic.values["packetsSent"] as? NSNumber)?.uint64Value ?? 0
                    }
                }
                continuation.resume(
                    returning: CallMediaActivity(
                        inboundPackets: inboundPackets,
                        outboundPackets: outboundPackets
                    )
                )
            }
        }
    }

    private func finishConnectivityWait(with result: Result<Void, CallMediaSessionError>) {
        guard let continuation = connectivityContinuation else { return }
        connectivityContinuation = nil
        connectivityTimeoutTask?.cancel()
        connectivityTimeoutTask = nil
        continuation.resume(with: result.mapError { $0 as Error })
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
        connectivityHandler = nil
        finishConnectivityWait(with: .failure(.closed))
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
              !description.contains("m=video"),
              gatheringGate.containsUsableCandidate(in: description) else {
            Self.logger.error("WebRTC local description is missing required audio or ICE data")
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
            Self.logger.info(
                "WebRTC offer gathering finished; candidates: \(self.gatheringGate.observedCandidateCount, privacy: .public), usable: \(self.gatheringGate.usableCandidateCount, privacy: .public)"
            )
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

    private func handleGeneratedCandidate(_ candidateSDP: String) {
        let descriptor = gatheringGate.observe(candidateSDP: candidateSDP)
        Self.logger.info(
            "WebRTC ICE candidate generated; transport: \(descriptor.transport ?? "unknown", privacy: .public), type: \(descriptor.type ?? "unknown", privacy: .public), usable: \(descriptor.isUsable, privacy: .public)"
        )
        if descriptor.isUsable {
            finishPendingOffer()
        }
    }
}

struct ICECandidateDescriptor: Equatable {
    let transport: String?
    let type: String?
    let isUsable: Bool
}

struct ICEGatheringGate {
    let requiresRelayCandidate: Bool
    private(set) var observedCandidateCount = 0
    private(set) var usableCandidateCount = 0

    var hasUsableCandidate: Bool { usableCandidateCount > 0 }

    mutating func observe(candidateSDP: String) -> ICECandidateDescriptor {
        observedCandidateCount += 1
        let fields = Self.candidateFields(candidateSDP)
        let transport = fields.count > 2 ? fields[2].lowercased() : nil
        let typeIndex = fields.firstIndex(where: { $0.caseInsensitiveCompare("typ") == .orderedSame })
        let type = typeIndex.flatMap { index in
            fields.indices.contains(index + 1) ? fields[index + 1].lowercased() : nil
        }
        let isUsable = type != nil && (!requiresRelayCandidate || type == "relay")
        if isUsable {
            usableCandidateCount += 1
        }
        return ICECandidateDescriptor(
            transport: transport,
            type: type,
            isUsable: isUsable
        )
    }

    func containsUsableCandidate(in description: String) -> Bool {
        description
            .split(whereSeparator: \Character.isNewline)
            .map(String.init)
            .filter { $0.hasPrefix("a=candidate:") }
            .contains { line in
                let fields = Self.candidateFields(line)
                guard let typeIndex = fields.firstIndex(where: {
                    $0.caseInsensitiveCompare("typ") == .orderedSame
                }), fields.indices.contains(typeIndex + 1) else {
                    return false
                }
                return !requiresRelayCandidate || fields[typeIndex + 1].caseInsensitiveCompare("relay") == .orderedSame
            }
    }

    private static func candidateFields(_ candidateSDP: String) -> [String] {
        let normalized = candidateSDP.hasPrefix("a=")
            ? String(candidateSDP.dropFirst(2))
            : candidateSDP
        return normalized.split(whereSeparator: \Character.isWhitespace).map(String.init)
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
    ) {
        let state: CallMediaConnectivityState?
        switch newState {
        case .connected, .completed:
            state = .connected
        case .disconnected:
            state = .disconnected
        case .failed:
            state = .failed
        default:
            state = nil
        }
        guard let state else { return }
        Task { @MainActor [weak self] in
            Self.logger.info(
                "WebRTC ICE connection state changed: \(String(describing: newState), privacy: .public)"
            )
            self?.connectivityHandler?(state)
            if state == .connected {
                self?.finishConnectivityWait(with: .success(()))
            } else if state == .failed {
                self?.finishConnectivityWait(with: .failure(.connectivityTimedOut))
            }
        }
    }

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didChange newState: RTCIceGatheringState
    ) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            Self.logger.info(
                "WebRTC ICE gathering state changed: \(String(describing: newState), privacy: .public)"
            )
            if newState == .complete {
                if gatheringGate.hasUsableCandidate {
                    finishPendingOffer()
                } else {
                    Self.logger.error("WebRTC ICE gathering completed without a usable candidate")
                    failPendingOffer(with: .offerCreationFailed)
                }
            }
        }
    }

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didGenerate candidate: RTCIceCandidate
    ) {
        let candidateSDP = candidate.sdp
        Task { @MainActor [weak self] in
            self?.handleGeneratedCandidate(candidateSDP)
        }
    }

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didRemove candidates: [RTCIceCandidate]
    ) {}

    nonisolated func peerConnection(
        _ peerConnection: RTCPeerConnection,
        didOpen dataChannel: RTCDataChannel
    ) {}
}
