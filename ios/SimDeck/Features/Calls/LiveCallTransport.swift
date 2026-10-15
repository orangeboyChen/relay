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
    private let waitBeforePollingAgain: () async throws -> Void
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?
    private var callLines: [UUID: UUID] = [:]

    init(
        api: any EncryptedCallCommandAPI,
        now: @escaping () -> Date = Date.init,
        makeCommandID: @escaping () -> UUID = UUID.init,
        makeSenderKey: @escaping () -> Curve25519.KeyAgreement.PrivateKey = {
            Curve25519.KeyAgreement.PrivateKey()
        },
        waitBeforePollingAgain: @escaping () async throws -> Void = {
            try await Task.sleep(for: .milliseconds(500))
        }
    ) {
        self.api = api
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.waitBeforePollingAgain = waitBeforePollingAgain
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        try await send(operation: "call.start", callID: callID, lineID: lineID, number: number)
        callLines[callID] = lineID
        eventHandler?(.ringing(callID: callID))
    }

    func endCall(callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        try await send(operation: "call.end", callID: callID, lineID: lineID)
        callLines.removeValue(forKey: callID)
        eventHandler?(.ended(callID: callID, reason: .remoteEnded))
    }

    func setHeld(_ isHeld: Bool, callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        try await send(operation: isHeld ? "call.hold" : "call.resume", callID: callID, lineID: lineID)
    }

    func setMuted(_ isMuted: Bool, callID: UUID) async throws {}

    func playDTMF(_ digits: String, callID: UUID) async throws {
        guard let lineID = callLines[callID] else { throw LiveCallTransportError.invalidResponse }
        try await send(operation: "call.dtmf", callID: callID, lineID: lineID, digits: digits)
    }

    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async { callLines.removeAll() }

    private func send(
        operation: String,
        callID: UUID,
        lineID: UUID,
        number: String? = nil,
        digits: String? = nil
    ) async throws {
        let deviceKeyData = try await api.commandRecipient(deviceID: lineID)
        let commandID = makeCommandID()
        let senderKey = makeSenderKey()
        let payload = CallCommandPayload(
            version: 1,
            operation: operation,
            callID: callID.uuidString.lowercased(),
            number: number,
            digits: digits
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
                return
            }
            if attempt < 59 {
                try await waitBeforePollingAgain()
            }
        }
        throw LiveCallTransportError.commandTimedOut
    }
}

struct CallCommandPayload: Codable, Equatable, Sendable {
    let version: Int
    let operation: String
    let callID: String
    let number: String?
    let digits: String?

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case callID = "call_id"
        case number
        case digits
    }
}

struct CallCommandResult: Codable, Equatable, Sendable {
    let version: Int
    let success: Bool
    let errorCode: String?
    let callState: String?

    enum CodingKeys: String, CodingKey {
        case version
        case success
        case errorCode = "error_code"
        case callState = "call_state"
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
