import AVFAudio
import CryptoKit
import Foundation
import OSLog

enum LiveCallTransportError: LocalizedError, Equatable {
    case missingInstallationIdentity
    case invalidDeviceKey
    case invalidResponse
    case commandTimedOut
    case modemRejected(String)

    var errorDescription: String? {
        switch self {
        case .missingInstallationIdentity:
            String(localized: "The selected SIM cannot receive encrypted commands.")
        case .invalidDeviceKey:
            String(localized: "The selected SIM has an invalid encryption key.")
        case .invalidResponse:
            String(localized: "The device returned an invalid encrypted response.")
        case .commandTimedOut:
            String(localized: "The device did not respond in time.")
        case .modemRejected:
            String(localized: "The modem rejected the call command.")
        }
    }
}

@MainActor
final class InstallationCommandSigner {
    var identity: InstallationIdentity?

    init(identity: InstallationIdentity? = nil) {
        self.identity = identity
    }
}

@MainActor
final class LiveCallTransport: CallTransport, CallLineRouting {
    private static let logger = Logger(
        subsystem: "com.nowcent.simdeck",
        category: "LiveCallTransport"
    )

    let capabilities = CallCapabilities.cellular

    private let api: any EncryptedCallCommandAPI
    private let now: () -> Date
    private let makeCommandID: () -> UUID
    private let makeSenderKey: () -> Curve25519.KeyAgreement.PrivateKey
    private let makeMediaSession: @MainActor () throws -> any CallMediaSession
    private let makeRelayMediaSession: @MainActor ([CallTURNCredentials]) throws -> any CallMediaSession
    private let waitBeforePollingAgain: () async throws -> Void
    private let automaticallyPollEvents: Bool
    private let eventStreamOpener: (any EncryptedEventStreamOpening)?
    private let commandSigner: InstallationCommandSigner
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?
    private var callLines: [UUID: UUID] = [:]
    private var callEvents: [UUID: CallEventContext] = [:]
    private var mediaSessions: [UUID: any CallMediaSession] = [:]
    private var activeAudioSession: AVAudioSession?
    private var eventCursor: Int64 = 0
    private var eventTask: Task<Void, Never>?
    private var activeEventStream: (any EncryptedEventStream)?
    private var lineRoutes: [UUID: DeviceLineIdentity] = [:]

    init(
        api: any EncryptedCallCommandAPI,
        commandSigner: InstallationCommandSigner,
        now: @escaping () -> Date = Date.init,
        makeCommandID: @escaping () -> UUID = UUID.init,
        makeSenderKey: @escaping () -> Curve25519.KeyAgreement.PrivateKey = {
            Curve25519.KeyAgreement.PrivateKey()
        },
        makeMediaSession: @escaping @MainActor () throws -> any CallMediaSession = {
            try WebRTCCallMediaSession()
        },
        makeRelayMediaSession: @escaping @MainActor ([CallTURNCredentials]) throws -> any CallMediaSession = { servers in
            try WebRTCCallMediaSession(iceServers: servers)
        },
        waitBeforePollingAgain: @escaping () async throws -> Void = {
            try await Task.sleep(for: .milliseconds(500))
        },
        automaticallyPollEvents: Bool = true,
        eventStreamOpener: (any EncryptedEventStreamOpening)? = nil
    ) {
        self.api = api
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.makeMediaSession = makeMediaSession
        self.makeRelayMediaSession = makeRelayMediaSession
        self.waitBeforePollingAgain = waitBeforePollingAgain
        self.automaticallyPollEvents = automaticallyPollEvents
        self.eventStreamOpener = eventStreamOpener ?? (api as? any EncryptedEventStreamOpening)
        self.commandSigner = commandSigner
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {
        var routes: [UUID: DeviceLineIdentity] = [:]
        for line in lines {
            routes[line.id] = line
            if let slotID = line.slotID {
                routes[slotID] = line
            }
            if let simID = line.simID {
                routes[simID] = line
            }
        }
        lineRoutes = routes
    }

    func deviceID(for lineID: UUID) -> UUID {
        lineRoutes[lineID]?.deviceID ?? lineID
    }

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {
        guard call.callSecret.count == 32, call.verificationKey.count == 32 else {
            throw LiveCallTransportError.invalidDeviceKey
        }
        callLines[call.callID] = call.lineID
        callEvents[call.callID] = CallEventContext(
            lineID: call.lineID,
            deviceID: deviceID(for: call.lineID),
            sharedSecret: call.callSecret,
            verificationKey: call.verificationKey,
            hasConnected: false
        )
        if automaticallyPollEvents {
            startEventLoopIfNeeded()
        }
    }

    func discardIncomingCall(callID: UUID) {
        mediaSessions.removeValue(forKey: callID)?.close()
        callLines.removeValue(forKey: callID)
        callEvents.removeValue(forKey: callID)
        stopEventLoopIfIdle()
    }

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        let deviceID = deviceID(for: lineID)
        Self.logger.info(
            "Outgoing call setup started; call: \(callID.uuidString, privacy: .public), line: \(lineID.uuidString, privacy: .public), device: \(deviceID.uuidString, privacy: .public)"
        )
        let verificationKey = try await api.eventVerificationKey(deviceID: deviceID)
        guard verificationKey.count == 32 else { throw LiveCallTransportError.invalidDeviceKey }
        Self.logger.info("Device verification key loaded for call \(callID.uuidString, privacy: .public)")
        let mediaSession = try await createMediaSession()
        Self.logger.info("WebRTC media session created for call \(callID.uuidString, privacy: .public)")
        var remoteCallStarted = false
        do {
            let offer = try await mediaSession.makeOffer()
            Self.logger.info(
                "WebRTC offer ready; enqueueing call.start for call \(callID.uuidString, privacy: .public)"
            )
            let exchange = try await send(
                operation: "call.start",
                callID: callID,
                lineID: lineID,
                number: number,
                mediaOffer: offer
            )
            remoteCallStarted = true
            guard let answer = exchange.result.mediaAnswer else {
                throw LiveCallTransportError.invalidResponse
            }
            Self.logger.info("Encrypted call.start completed for call \(callID.uuidString, privacy: .public)")
            try await mediaSession.applyAnswer(answer)
            Self.logger.info("WebRTC answer applied for call \(callID.uuidString, privacy: .public)")

            callLines[callID] = lineID
            callEvents[callID] = CallEventContext(
                lineID: lineID,
                deviceID: deviceID,
                sharedSecret: exchange.sharedSecret.withUnsafeBytes { Data($0) },
                verificationKey: verificationKey,
                hasConnected: false
            )
            installMediaSession(mediaSession, for: callID)
            if automaticallyPollEvents {
                startEventLoopIfNeeded()
            }
        } catch {
            Self.logger.error(
                "Outgoing call setup failed; call: \(callID.uuidString, privacy: .public), stage error: \(String(reflecting: error), privacy: .public)"
            )
            mediaSession.close()
            if remoteCallStarted {
                _ = try? await send(operation: "call.end", callID: callID, lineID: lineID)
            }
            throw error
        }
    }

    func answerIncomingCall(callID: UUID) async throws {
        guard let lineID = callLines[callID], callEvents[callID] != nil else {
            throw LiveCallTransportError.invalidResponse
        }
        let mediaSession = try await createMediaSession()
        var remoteCallAnswered = false
        do {
            let offer = try await mediaSession.makeOffer()
            let exchange = try await send(
                operation: "call.answer",
                callID: callID,
                lineID: lineID,
                mediaOffer: offer
            )
            remoteCallAnswered = true
            guard let answer = exchange.result.mediaAnswer else {
                throw LiveCallTransportError.invalidResponse
            }
            try await mediaSession.applyAnswer(answer)
            installMediaSession(mediaSession, for: callID)
        } catch {
            mediaSession.close()
            if remoteCallAnswered {
                _ = try? await send(operation: "call.end", callID: callID, lineID: lineID)
            }
            throw error
        }
    }

    func rejectIncomingCall(callID: UUID) async throws {
        guard let lineID = callLines[callID] else {
            throw LiveCallTransportError.invalidResponse
        }
        defer { discardIncomingCall(callID: callID) }
        _ = try await send(operation: "call.reject", callID: callID, lineID: lineID)
    }

    func endCall(callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        defer {
            mediaSessions.removeValue(forKey: callID)?.close()
            callLines.removeValue(forKey: callID)
            callEvents.removeValue(forKey: callID)
            stopEventLoopIfIdle()
        }
        _ = try await send(operation: "call.end", callID: callID, lineID: lineID)
    }

    func setHeld(_ isHeld: Bool, callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        _ = try await send(operation: isHeld ? "call.hold" : "call.resume", callID: callID, lineID: lineID)
    }

    func setMuted(_ isMuted: Bool, callID: UUID) async throws {
        guard let mediaSession = mediaSessions[callID] else {
            throw LiveCallTransportError.invalidResponse
        }
        mediaSession.setMuted(isMuted)
    }

    func playDTMF(_ digits: String, callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        _ = try await send(operation: "call.dtmf", callID: callID, lineID: lineID, digits: digits)
    }

    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {
        activeAudioSession = audioSession
        for mediaSession in mediaSessions.values {
            mediaSession.audioSessionDidActivate(audioSession)
        }
    }

    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {
        for mediaSession in mediaSessions.values {
            mediaSession.audioSessionDidDeactivate(audioSession)
        }
        if activeAudioSession === audioSession {
            activeAudioSession = nil
        }
    }

    func reset() async {
        eventTask?.cancel()
        eventTask = nil
        activeEventStream?.close()
        activeEventStream = nil
        for mediaSession in mediaSessions.values {
            mediaSession.close()
        }
        mediaSessions.removeAll()
        activeAudioSession = nil
        callLines.removeAll()
        callEvents.removeAll()
    }

    private func installMediaSession(_ mediaSession: any CallMediaSession, for callID: UUID) {
        mediaSessions[callID] = mediaSession
        if let activeAudioSession {
            mediaSession.audioSessionDidActivate(activeAudioSession)
        }
    }

    private func send(
        operation: String,
        callID: UUID,
        lineID: UUID,
        number: String? = nil,
        digits: String? = nil,
        mediaOffer: String? = nil
    ) async throws -> CallCommandExchange {
        let line = lineRoutes[lineID] ?? DeviceLineIdentity(
            id: lineID,
            numericID: 1,
            deviceName: "",
            lineName: "",
            phoneNumber: "",
            isOnline: true
        )
        let target = try await api.commandTarget(for: line)
        let deviceID = target.deviceID
        let commandID = makeCommandID()
        let senderKey = makeSenderKey()
        guard let installationIdentity = commandSigner.identity else {
            throw LiveCallTransportError.missingInstallationIdentity
        }
        let payload = CallCommandPayload(
            version: 1,
            operation: operation,
            callID: callID.uuidString.lowercased(),
            slotID: target.slotID,
            simID: target.simID,
            number: number,
            digits: digits,
            mediaOffer: mediaOffer
        )
        let sealed: SealedCallCommand
        do {
            sealed = try EncryptedCallCommandCodec.seal(
                payload: payload,
                recipientPublicKey: target.publicKey,
                senderPrivateKey: senderKey,
                nonce: AES.GCM.Nonce(),
                commandID: commandID,
                deviceID: deviceID,
                createdAt: Date(timeIntervalSince1970: floor(now().timeIntervalSince1970)),
                installationIdentity: installationIdentity
            )
        } catch is CryptoKitError {
            throw LiveCallTransportError.invalidDeviceKey
        }
        Self.logger.info(
            "Encrypted device command prepared; operation: \(operation, privacy: .public), command: \(commandID.uuidString, privacy: .public), device: \(deviceID.uuidString, privacy: .public)"
        )
        try await api.enqueueCommand(deviceID: deviceID, command: sealed.request)
        Self.logger.info(
            "Encrypted device command enqueued; operation: \(operation, privacy: .public), command: \(commandID.uuidString, privacy: .public)"
        )

        var lastState: String?
        for attempt in 0..<60 {
            let status = try await api.commandStatus(deviceID: deviceID, commandID: commandID)
            if status.state != lastState {
                Self.logger.info(
                    "Encrypted device command state changed; command: \(commandID.uuidString, privacy: .public), state: \(status.state, privacy: .public), attempt: \(attempt + 1, privacy: .public)"
                )
                lastState = status.state
            }
            if status.state == "completed" {
                guard let encryptedResult = status.result else {
                    throw LiveCallTransportError.invalidResponse
                }
                let result: CallCommandResult
                do {
                    result = try EncryptedCallCommandCodec.openResult(
                        encryptedResult,
                        sharedSecret: sealed.sharedSecret,
                        commandID: commandID,
                        deviceID: deviceID
                    )
                } catch {
                    throw LiveCallTransportError.invalidResponse
                }
                guard result.success else {
                    throw LiveCallTransportError.modemRejected(result.errorCode ?? "unknown")
                }
                return CallCommandExchange(
                    sharedSecret: sealed.sharedSecret,
                    result: result
                )
            }
            if attempt < 59 {
                try await waitBeforePollingAgain()
            }
        }
        throw LiveCallTransportError.commandTimedOut
    }

    private func createMediaSession() async throws -> any CallMediaSession {
        guard let provider = api as? any CallTURNCredentialProviding else {
            Self.logger.info("Creating direct WebRTC media session without TURN")
            return try makeMediaSession()
        }
        Self.logger.info("Fetching TURN credentials for WebRTC media session")
        let credentials = try await provider.turnCredentials()
        Self.logger.info(
            "TURN credentials fetched; URL count: \(credentials.urls.count, privacy: .public), expiration: \(credentials.expiresAt.timeIntervalSince1970, privacy: .public)"
        )
        return try makeRelayMediaSession([credentials])
    }

    func synchronizeEvents() async throws {
        let page = try await api.encryptedEvents(afterSequence: eventCursor)
        for envelope in page.events {
            process(envelope)
        }
        eventCursor = max(eventCursor, page.nextSequence)
    }

    private func startEventLoopIfNeeded() {
        guard eventTask == nil else { return }
        eventTask = Task { [weak self] in
            await self?.runEventLoop()
        }
    }

    private func runEventLoop() async {
        var consecutiveFailures = 0
        while !callEvents.isEmpty && !Task.isCancelled {
            do {
                if let eventStreamOpener {
                    let stream = try await eventStreamOpener.openEncryptedEventStream(
                        afterSequence: eventCursor
                    )
                    activeEventStream = stream
                    while !callEvents.isEmpty && !Task.isCancelled {
                        let page = try await stream.receive()
                        for envelope in page.events {
                            process(envelope)
                        }
                        try await stream.acknowledge(sequence: page.nextSequence)
                        eventCursor = max(eventCursor, page.nextSequence)
                        consecutiveFailures = 0
                    }
                    stream.close()
                    activeEventStream = nil
                } else {
                    try await synchronizeEvents()
                    consecutiveFailures = 0
                    try await waitBeforePollingAgain()
                }
            } catch is CancellationError {
                activeEventStream?.close()
                activeEventStream = nil
                return
            } catch {
                activeEventStream?.close()
                activeEventStream = nil
                consecutiveFailures += 1
                if consecutiveFailures >= 3 {
                    failActiveCalls()
                    return
                }
                do {
                    try await waitBeforePollingAgain()
                } catch {
                    return
                }
            }
        }
        activeEventStream?.close()
        activeEventStream = nil
        eventTask = nil
    }

    private func process(_ envelope: EncryptedEventEnvelope) {
        guard envelope.type == "call.state" else { return }
        for callID in Array(callEvents.keys) {
            guard var context = callEvents[callID], context.deviceID == envelope.deviceUUID else {
                continue
            }
            guard let payload = try? EncryptedCallEventCodec.open(
                envelope,
                sharedSecret: context.sharedSecret,
                verificationKey: context.verificationKey
            ), payload.callID == callID else {
                continue
            }
            switch payload.state {
            case "ringing":
                eventHandler?(.ringing(callID: callID))
            case "active":
                guard !context.hasConnected else { return }
                context.hasConnected = true
                callEvents[callID] = context
                eventHandler?(.connected(callID: callID, at: envelope.occurredAt))
            case "ended":
                let reason: CallEndReason = context.hasConnected ? .remoteEnded : .unanswered
                mediaSessions.removeValue(forKey: callID)?.close()
                callEvents.removeValue(forKey: callID)
                callLines.removeValue(forKey: callID)
                eventHandler?(.ended(callID: callID, reason: reason))
            case "dialing", "held", "waiting":
                break
            default:
                break
            }
            return
        }
    }

    private func failActiveCalls() {
        let callIDs = Array(callEvents.keys)
        for mediaSession in mediaSessions.values {
            mediaSession.close()
        }
        mediaSessions.removeAll()
        callEvents.removeAll()
        callLines.removeAll()
        for callID in callIDs {
            eventHandler?(.ended(callID: callID, reason: .failed))
        }
        eventTask = nil
    }

    private func stopEventLoopIfIdle() {
        guard callEvents.isEmpty else { return }
        activeEventStream?.close()
        activeEventStream = nil
        eventTask?.cancel()
        eventTask = nil
    }
}

private struct CallCommandExchange {
    let sharedSecret: SharedSecret
    let result: CallCommandResult
}

private struct CallEventContext {
    let lineID: UUID
    let deviceID: UUID
    let sharedSecret: Data
    let verificationKey: Data
    var hasConnected: Bool
}

struct CallStateEventPayload: Codable, Equatable, Sendable {
    let version: Int
    let callID: UUID
    let state: String
    let active: Bool
    let held: Bool
    let waiting: Bool

    enum CodingKeys: String, CodingKey {
        case version
        case callID = "call_id"
        case state
        case active
        case held
        case waiting
    }
}

struct CallCommandPayload: Codable, Equatable, Sendable {
    let version: Int
    let operation: String
    let callID: String
    let slotID: UUID?
    let simID: UUID?
    let number: String?
    let digits: String?
    let mediaOffer: String?

    init(
        version: Int,
        operation: String,
        callID: String,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        number: String? = nil,
        digits: String? = nil,
        mediaOffer: String? = nil
    ) {
        self.version = version
        self.operation = operation
        self.callID = callID
        self.slotID = slotID
        self.simID = simID
        self.number = number
        self.digits = digits
        self.mediaOffer = mediaOffer
    }

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case callID = "call_id"
        case slotID = "slot_id"
        case simID = "sim_id"
        case number
        case digits
        case mediaOffer = "media_offer"
    }

    func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        try values.encode(version, forKey: .version)
        try values.encode(operation, forKey: .operation)
        try values.encode(callID, forKey: .callID)
        try values.encodeIfPresent(slotID, forKey: .slotID)
        try values.encodeIfPresent(simID, forKey: .simID)
        try values.encodeIfPresent(number, forKey: .number)
        try values.encodeIfPresent(digits, forKey: .digits)
        try values.encodeIfPresent(mediaOffer, forKey: .mediaOffer)
    }
}

struct CallCommandResult: Codable, Equatable, Sendable {
    let version: Int
    let success: Bool
    let errorCode: String?
    let callState: String?
    let mediaAnswer: String?
    let slotSettings: SlotSettings?

    init(
        version: Int,
        success: Bool,
        errorCode: String? = nil,
        callState: String? = nil,
        mediaAnswer: String? = nil,
        slotSettings: SlotSettings? = nil
    ) {
        self.version = version
        self.success = success
        self.errorCode = errorCode
        self.callState = callState
        self.mediaAnswer = mediaAnswer
        self.slotSettings = slotSettings
    }

    enum CodingKeys: String, CodingKey {
        case version
        case success
        case errorCode = "error_code"
        case callState = "call_state"
        case mediaAnswer = "media_answer"
        case slotSettings = "slot_settings"
    }
}

struct SealedCallCommand {
    let request: EncryptedCallCommandRequest
    let sharedSecret: SharedSecret
}

enum EncryptedCallCommandCodec {
    static func seal<Payload: Encodable>(
        payload: Payload,
        recipientPublicKey: Data,
        senderPrivateKey: Curve25519.KeyAgreement.PrivateKey,
        nonce: AES.GCM.Nonce,
        commandID: UUID,
        deviceID: UUID,
        createdAt: Date,
        installationIdentity: InstallationIdentity
    ) throws -> SealedCallCommand {
        let deviceKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: recipientPublicKey)
        let sharedSecret = try senderPrivateKey.sharedSecretFromKeyAgreement(with: deviceKey)
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let plaintext = try encoder.encode(payload)
        let key = key(
            sharedSecret: sharedSecret,
            commandID: commandID,
            deviceID: deviceID,
            context: "sim-deck-command-v1"
        )
        let sealed = try AES.GCM.seal(
            plaintext,
            using: key,
            nonce: nonce,
            authenticating: associatedData(
                context: "sim-deck-command-v1",
                commandID: commandID,
                deviceID: deviceID
            )
        )
        let senderPublicKey = senderPrivateKey.publicKey.rawRepresentation
        let nonceData = nonce.withUnsafeBytes { Data($0) }
        let ciphertext = sealed.ciphertext + sealed.tag
        let expiresAt = createdAt.addingTimeInterval(60)
        let signingKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: installationIdentity.signingPrivateKey
        )
        let unsignedRequest = EncryptedCallCommandRequest(
            id: commandID,
            senderInstallationID: installationIdentity.id,
            senderPublicKey: senderPublicKey,
            nonce: nonceData,
            ciphertext: ciphertext,
            signature: Data(),
            createdAt: createdAt,
            expiresAt: expiresAt
        )
        let signature = try signingKey.signature(
            for: signedData(for: unsignedRequest, deviceID: deviceID)
        )
        return SealedCallCommand(
            request: EncryptedCallCommandRequest(
                id: commandID,
                senderInstallationID: installationIdentity.id,
                senderPublicKey: senderPublicKey,
                nonce: nonceData,
                ciphertext: ciphertext,
                signature: signature,
                createdAt: createdAt,
                expiresAt: expiresAt
            ),
            sharedSecret: sharedSecret
        )
    }

    static func openResult(
        _ encrypted: EncryptedCallCommandResult,
        sharedSecret: SharedSecret,
        commandID: UUID,
        deviceID: UUID
    ) throws -> CallCommandResult {
        guard encrypted.nonce.count == 12, encrypted.ciphertext.count > 16 else {
            throw LiveCallTransportError.invalidResponse
        }
        let key = key(
            sharedSecret: sharedSecret,
            commandID: commandID,
            deviceID: deviceID,
            context: "sim-deck-command-result-v1"
        )
        let box = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: encrypted.nonce),
            ciphertext: Data(encrypted.ciphertext.dropLast(16)),
            tag: Data(encrypted.ciphertext.suffix(16))
        )
        let plaintext = try AES.GCM.open(
            box,
            using: key,
            authenticating: associatedData(
                context: "sim-deck-command-result-v1",
                commandID: commandID,
                deviceID: deviceID
            )
        )
        let result = try JSONDecoder().decode(CallCommandResult.self, from: plaintext)
        guard result.version == 1 else { throw LiveCallTransportError.invalidResponse }
        return result
    }

    private static func key(
        sharedSecret: SharedSecret,
        commandID: UUID,
        deviceID: UUID,
        context: String
    ) -> SymmetricKey {
        sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data(context.utf8),
            outputByteCount: 32
        )
    }

    private static func associatedData(context: String, commandID: UUID, deviceID: UUID) -> Data {
        Data("\(context)\0\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8)
    }

    static func signedData(
        for request: EncryptedCallCommandRequest,
        deviceID: UUID
    ) -> Data {
        var data = Data()
        for field in [
            Data("sim-deck-command-envelope-v2".utf8),
            Data(request.id.uuidString.lowercased().utf8),
            Data(deviceID.uuidString.lowercased().utf8),
            Data(request.senderInstallationID.uuidString.lowercased().utf8),
            request.senderPublicKey,
            request.nonce,
            request.ciphertext,
        ] {
            appendCommandField(field, to: &data)
        }
        appendCommandInteger(Int64(request.createdAt.timeIntervalSince1970 * 1_000), to: &data)
        appendCommandInteger(Int64(request.expiresAt.timeIntervalSince1970 * 1_000), to: &data)
        return data
    }

    private static func appendCommandField(_ field: Data, to data: inout Data) {
        var length = UInt32(field.count).bigEndian
        withUnsafeBytes(of: &length) { data.append(contentsOf: $0) }
        data.append(field)
    }

    private static func appendCommandInteger(_ value: Int64, to data: inout Data) {
        var encoded = UInt64(bitPattern: value).bigEndian
        withUnsafeBytes(of: &encoded) { data.append(contentsOf: $0) }
    }
}

enum EncryptedCallEventCodec {
    static func open(
        _ envelope: EncryptedEventEnvelope,
        sharedSecret: Data,
        verificationKey: Data
    ) throws -> CallStateEventPayload {
        guard envelope.type == "call.state",
              envelope.keyEpoch == 1,
              envelope.nonce.count == 12,
              envelope.ciphertext.count > 16,
              envelope.signature.count == 64 else {
            throw LiveCallTransportError.invalidResponse
        }
        let publicKey = try Curve25519.Signing.PublicKey(rawRepresentation: verificationKey)
        guard publicKey.isValidSignature(envelope.signature, for: signedData(envelope)) else {
            throw LiveCallTransportError.invalidResponse
        }
        let key = HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: sharedSecret),
            salt: Data("\(envelope.id.uuidString.lowercased())\0\(envelope.deviceUUID.uuidString.lowercased())".utf8),
            info: Data("sim-deck-call-event-v1".utf8),
            outputByteCount: 32
        )
        let box = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: envelope.nonce),
            ciphertext: Data(envelope.ciphertext.dropLast(16)),
            tag: Data(envelope.ciphertext.suffix(16))
        )
        let plaintext = try AES.GCM.open(
            box,
            using: key,
            authenticating: associatedData(envelope)
        )
        let payload = try JSONDecoder().decode(CallStateEventPayload.self, from: plaintext)
        guard payload.version == 1 else { throw LiveCallTransportError.invalidResponse }
        return payload
    }

    static func associatedData(_ envelope: EncryptedEventEnvelope) -> Data {
        var data = Data()
        data.appendLengthPrefixed(Data(envelope.id.uuidString.lowercased().utf8))
        data.appendLengthPrefixed(Data(envelope.type.utf8))
        data.appendLengthPrefixed(Data(envelope.deviceUUID.uuidString.lowercased().utf8))
        if let recipientInstallationID = envelope.recipientInstallationID {
            data.appendLengthPrefixed(Data(recipientInstallationID.uuidString.lowercased().utf8))
        }
        // Device events are signed after their timestamps are truncated to milliseconds.
        // Round at that wire-protocol precision before converting to nanoseconds so Date's
        // floating-point representation cannot change the authenticated bytes.
        let milliseconds = Int64((envelope.occurredAt.timeIntervalSince1970 * 1_000).rounded())
        let nanoseconds = milliseconds * 1_000_000
        data.appendBigEndian(UInt64(bitPattern: nanoseconds))
        data.appendBigEndian(UInt64(envelope.keyEpoch))
        return data
    }

    static func signedData(_ envelope: EncryptedEventEnvelope) -> Data {
        var data = associatedData(envelope)
        data.appendLengthPrefixed(envelope.nonce)
        data.appendLengthPrefixed(envelope.ciphertext)
        return data
    }
}

private extension Data {
    mutating func appendLengthPrefixed(_ value: Data) {
        appendBigEndian(UInt32(value.count))
        append(value)
    }

    mutating func appendBigEndian<T: FixedWidthInteger>(_ value: T) {
        var bigEndian = value.bigEndian
        Swift.withUnsafeBytes(of: &bigEndian) { bytes in
            append(contentsOf: bytes)
        }
    }
}
