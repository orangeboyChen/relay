import AVFAudio
import CryptoKit
import Foundation

enum LiveCallTransportError: LocalizedError, Equatable {
    case invalidDeviceKey
    case invalidResponse
    case commandTimedOut
    case modemRejected(String)

    var errorDescription: String? {
        switch self {
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
final class LiveCallTransport: CallTransport {
    let capabilities = CallCapabilities.cellular

    private let api: any EncryptedCallCommandAPI
    private let now: () -> Date
    private let makeCommandID: () -> UUID
    private let makeSenderKey: () -> Curve25519.KeyAgreement.PrivateKey
    private let makeMediaSession: @MainActor () throws -> any CallMediaSession
    private let waitBeforePollingAgain: () async throws -> Void
    private let automaticallyPollEvents: Bool
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?
    private var callLines: [UUID: UUID] = [:]
    private var callEvents: [UUID: CallEventContext] = [:]
    private var mediaSessions: [UUID: any CallMediaSession] = [:]
    private var eventCursor: Int64 = 0
    private var eventTask: Task<Void, Never>?

    init(
        api: any EncryptedCallCommandAPI,
        now: @escaping () -> Date = Date.init,
        makeCommandID: @escaping () -> UUID = UUID.init,
        makeSenderKey: @escaping () -> Curve25519.KeyAgreement.PrivateKey = {
            Curve25519.KeyAgreement.PrivateKey()
        },
        makeMediaSession: @escaping @MainActor () throws -> any CallMediaSession = {
            try WebRTCCallMediaSession()
        },
        waitBeforePollingAgain: @escaping () async throws -> Void = {
            try await Task.sleep(for: .milliseconds(500))
        },
        automaticallyPollEvents: Bool = true
    ) {
        self.api = api
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.makeMediaSession = makeMediaSession
        self.waitBeforePollingAgain = waitBeforePollingAgain
        self.automaticallyPollEvents = automaticallyPollEvents
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {
        guard call.callSecret.count == 32, call.verificationKey.count == 32 else {
            throw LiveCallTransportError.invalidDeviceKey
        }
        callLines[call.callID] = call.lineID
        callEvents[call.callID] = CallEventContext(
            lineID: call.lineID,
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
        let verificationKey = try await api.eventVerificationKey(deviceID: lineID)
        guard verificationKey.count == 32 else { throw LiveCallTransportError.invalidDeviceKey }
        let mediaSession = try makeMediaSession()
        var remoteCallStarted = false
        do {
            let offer = try await mediaSession.makeOffer()
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
            try await mediaSession.applyAnswer(answer)

            callLines[callID] = lineID
            callEvents[callID] = CallEventContext(
                lineID: lineID,
                sharedSecret: exchange.sharedSecret.withUnsafeBytes { Data($0) },
                verificationKey: verificationKey,
                hasConnected: false
            )
            mediaSessions[callID] = mediaSession
            if automaticallyPollEvents {
                startEventLoopIfNeeded()
            }
        } catch {
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
        let mediaSession = try makeMediaSession()
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
            mediaSessions[callID] = mediaSession
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
        _ = try await send(operation: "call.reject", callID: callID, lineID: lineID)
        discardIncomingCall(callID: callID)
    }

    func endCall(callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        _ = try await send(operation: "call.end", callID: callID, lineID: lineID)
        mediaSessions.removeValue(forKey: callID)?.close()
        callLines.removeValue(forKey: callID)
        callEvents.removeValue(forKey: callID)
        stopEventLoopIfIdle()
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
        for mediaSession in mediaSessions.values {
            mediaSession.audioSessionDidActivate(audioSession)
        }
    }

    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {
        for mediaSession in mediaSessions.values {
            mediaSession.audioSessionDidDeactivate(audioSession)
        }
    }

    func reset() async {
        eventTask?.cancel()
        eventTask = nil
        for mediaSession in mediaSessions.values {
            mediaSession.close()
        }
        mediaSessions.removeAll()
        callLines.removeAll()
        callEvents.removeAll()
    }

    private func send(
        operation: String,
        callID: UUID,
        lineID: UUID,
        number: String? = nil,
        digits: String? = nil,
        mediaOffer: String? = nil
    ) async throws -> CallCommandExchange {
        let deviceKeyData = try await api.commandRecipient(deviceID: lineID)
        let commandID = makeCommandID()
        let senderKey = makeSenderKey()
        let payload = CallCommandPayload(
            version: 1,
            operation: operation,
            callID: callID.uuidString.lowercased(),
            number: number,
            digits: digits,
            mediaOffer: mediaOffer
        )
        let sealed: SealedCallCommand
        do {
            sealed = try EncryptedCallCommandCodec.seal(
                payload: payload,
                recipientPublicKey: deviceKeyData,
                senderPrivateKey: senderKey,
                nonce: AES.GCM.Nonce(),
                commandID: commandID,
                deviceID: lineID,
                createdAt: now()
            )
        } catch is CryptoKitError {
            throw LiveCallTransportError.invalidDeviceKey
        }
        try await api.enqueueCommand(deviceID: lineID, command: sealed.request)

        for attempt in 0..<60 {
            let status = try await api.commandStatus(deviceID: lineID, commandID: commandID)
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
                        deviceID: lineID
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
                try await synchronizeEvents()
                consecutiveFailures = 0
            } catch is CancellationError {
                return
            } catch {
                consecutiveFailures += 1
                if consecutiveFailures >= 3 {
                    failActiveCalls()
                    return
                }
            }
            do {
                try await waitBeforePollingAgain()
            } catch {
                return
            }
        }
        eventTask = nil
    }

    private func process(_ envelope: EncryptedEventEnvelope) {
        guard envelope.type == "call.state" else { return }
        for callID in Array(callEvents.keys) {
            guard var context = callEvents[callID], context.lineID == envelope.deviceUUID else {
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
                stopEventLoopIfIdle()
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
    let number: String?
    let digits: String?
    let mediaOffer: String?

    init(
        version: Int,
        operation: String,
        callID: String,
        number: String? = nil,
        digits: String? = nil,
        mediaOffer: String? = nil
    ) {
        self.version = version
        self.operation = operation
        self.callID = callID
        self.number = number
        self.digits = digits
        self.mediaOffer = mediaOffer
    }

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case callID = "call_id"
        case number
        case digits
        case mediaOffer = "media_offer"
    }
}

struct CallCommandResult: Codable, Equatable, Sendable {
    let version: Int
    let success: Bool
    let errorCode: String?
    let callState: String?
    let mediaAnswer: String?

    init(
        version: Int,
        success: Bool,
        errorCode: String? = nil,
        callState: String? = nil,
        mediaAnswer: String? = nil
    ) {
        self.version = version
        self.success = success
        self.errorCode = errorCode
        self.callState = callState
        self.mediaAnswer = mediaAnswer
    }

    enum CodingKeys: String, CodingKey {
        case version
        case success
        case errorCode = "error_code"
        case callState = "call_state"
        case mediaAnswer = "media_answer"
    }
}

struct SealedCallCommand {
    let request: EncryptedCallCommandRequest
    let sharedSecret: SharedSecret
}

enum EncryptedCallCommandCodec {
    static func seal(
        payload: CallCommandPayload,
        recipientPublicKey: Data,
        senderPrivateKey: Curve25519.KeyAgreement.PrivateKey,
        nonce: AES.GCM.Nonce,
        commandID: UUID,
        deviceID: UUID,
        createdAt: Date
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
        return SealedCallCommand(
            request: EncryptedCallCommandRequest(
                id: commandID,
                senderPublicKey: senderPrivateKey.publicKey.rawRepresentation,
                nonce: nonce.withUnsafeBytes { Data($0) },
                ciphertext: sealed.ciphertext + sealed.tag,
                createdAt: createdAt,
                expiresAt: createdAt.addingTimeInterval(60)
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
        let nanoseconds = Int64((envelope.occurredAt.timeIntervalSince1970 * 1_000_000_000).rounded())
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
