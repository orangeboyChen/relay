import CryptoKit
import Foundation

enum LiveMessageTransportError: LocalizedError, Equatable {
    case missingInstallationIdentity
    case invalidDeviceKey
    case invalidResponse
    case commandTimedOut
    case modemRejected(String)

    var errorDescription: String? {
        switch self {
        case .missingInstallationIdentity:
            String(localized: "This iPhone is not ready to send encrypted messages.")
        case .invalidDeviceKey:
            String(localized: "The selected SIM has an invalid encryption key.")
        case .invalidResponse:
            String(localized: "The device returned an invalid encrypted message response.")
        case .commandTimedOut:
            String(localized: "The device did not respond in time.")
        case .modemRejected:
            String(localized: "The modem rejected the message.")
        }
    }
}

struct IncomingMessageDelivery: Equatable, Sendable {
    let id: UUID
    let sourceID: String
    let lineID: UUID
    let remoteNumber: String
    let body: String
    let receivedAt: Date
}

@MainActor
protocol MessageTransport: AnyObject {
    func send(messageID: UUID, lineID: UUID, remoteNumber: String, body: String) async throws
    func synchronize(lines: [DeviceLineIdentity]) async throws -> [IncomingMessageDelivery]
    func updateLineRoutes(_ lines: [DeviceLineIdentity])
}

extension MessageTransport {
    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {}
}

@MainActor
protocol MessageCursorStoring: AnyObject {
    var sequence: Int64 { get set }
}

@MainActor
final class UserDefaultsMessageCursorStore: MessageCursorStoring {
    private let defaults: UserDefaults
    private let installationID: () -> UUID?

    init(
        defaults: UserDefaults = .standard,
        installationID: @escaping () -> UUID? = { nil }
    ) {
        self.defaults = defaults
        self.installationID = installationID
    }

    var sequence: Int64 {
        get { Int64(defaults.integer(forKey: key)) }
        set { defaults.set(newValue, forKey: key) }
    }

    private var key: String {
        let suffix = installationID()?.uuidString.lowercased() ?? "pending"
        return "sim-deck.message-cursor.v1.\(suffix)"
    }
}

@MainActor
final class LiveMessageTransport: MessageTransport {
    private let api: any EncryptedCallCommandAPI
    private let commandSigner: InstallationCommandSigner
    private let cursorStore: any MessageCursorStoring
    private let now: () -> Date
    private let makeCommandID: () -> UUID
    private let makeSenderKey: () -> Curve25519.KeyAgreement.PrivateKey
    private let waitBeforePollingAgain: () async throws -> Void
    private var lineRoutes: [UUID: DeviceLineIdentity] = [:]

    init(
        api: any EncryptedCallCommandAPI,
        commandSigner: InstallationCommandSigner,
        cursorStore: any MessageCursorStoring = UserDefaultsMessageCursorStore(),
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
        self.commandSigner = commandSigner
        self.cursorStore = cursorStore
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.waitBeforePollingAgain = waitBeforePollingAgain
    }

    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {
        lineRoutes = Dictionary(uniqueKeysWithValues: lines.map { ($0.id, $0) })
    }

    func send(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String
    ) async throws {
        guard let installationIdentity = commandSigner.identity else {
            throw LiveMessageTransportError.missingInstallationIdentity
        }
        let line = lineRoutes[lineID]
        let deviceID = line?.deviceID ?? lineID
        let recipientKey = try await api.commandRecipient(deviceID: deviceID)
        let commandID = makeCommandID()
        let payload = MessageCommandPayload(
            version: 1,
            operation: "message.send",
            messageID: messageID.uuidString.lowercased(),
            slotID: line?.slotID,
            simID: line?.simID,
            number: remoteNumber,
            body: body
        )
        let sealed: SealedCallCommand
        do {
            sealed = try EncryptedCallCommandCodec.seal(
                payload: payload,
                recipientPublicKey: recipientKey,
                senderPrivateKey: makeSenderKey(),
                nonce: AES.GCM.Nonce(),
                commandID: commandID,
                deviceID: deviceID,
                createdAt: Date(timeIntervalSince1970: floor(now().timeIntervalSince1970)),
                installationIdentity: installationIdentity
            )
        } catch is CryptoKitError {
            throw LiveMessageTransportError.invalidDeviceKey
        }
        try await api.enqueueCommand(deviceID: deviceID, command: sealed.request)

        for attempt in 0..<60 {
            let status = try await api.commandStatus(deviceID: deviceID, commandID: commandID)
            if status.state == "completed" {
                guard let encryptedResult = status.result else {
                    throw LiveMessageTransportError.invalidResponse
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
                    throw LiveMessageTransportError.invalidResponse
                }
                guard result.success else {
                    throw LiveMessageTransportError.modemRejected(
                        result.errorCode ?? "unknown"
                    )
                }
                return
            }
            if attempt < 59 {
                try await waitBeforePollingAgain()
            }
        }
        throw LiveMessageTransportError.commandTimedOut
    }

    func synchronize(lines: [DeviceLineIdentity]) async throws -> [IncomingMessageDelivery] {
        guard let installationIdentity = commandSigner.identity else {
            throw LiveMessageTransportError.missingInstallationIdentity
        }
        let page = try await api.encryptedEvents(afterSequence: cursorStore.sequence)
        let linesByDevice = Dictionary(grouping: lines, by: { $0.deviceID })
        var verificationKeys: [UUID: Data] = [:]
        var deliveries: [IncomingMessageDelivery] = []

        for envelope in page.events where envelope.type == "message.received" {
            guard let candidates = linesByDevice[envelope.deviceUUID],
                  let commandPublicKey = candidates.first?.commandPublicKey else {
                continue
            }
            let verificationKey: Data
            if let cached = verificationKeys[envelope.deviceUUID] {
                verificationKey = cached
            } else {
                verificationKey = try await api.eventVerificationKey(
                    deviceID: envelope.deviceUUID
                )
                verificationKeys[envelope.deviceUUID] = verificationKey
            }
            guard let payload = try? EncryptedMessageEventCodec.open(
                envelope,
                installationIdentity: installationIdentity,
                deviceCommandPublicKey: commandPublicKey,
                deviceVerificationKey: verificationKey
            ) else {
                continue
            }
            deliveries.append(
                IncomingMessageDelivery(
                    id: envelope.id,
                    sourceID: payload.messageID,
                    lineID: candidates.first(where: { $0.slotID == payload.slotID })?.id ?? payload.deviceUUID,
                    remoteNumber: payload.remoteNumber,
                    body: payload.body,
                    receivedAt: payload.receivedAt
                )
            )
        }
        cursorStore.sequence = max(cursorStore.sequence, page.nextSequence)
        return deliveries
    }
}

struct MessageCommandPayload: Codable, Equatable, Sendable {
    let version: Int
    let operation: String
    let messageID: String
    let slotID: UUID?
    let simID: UUID?
    let number: String
    let body: String

    init(
        version: Int,
        operation: String,
        messageID: String,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        number: String,
        body: String
    ) {
        self.version = version
        self.operation = operation
        self.messageID = messageID
        self.slotID = slotID
        self.simID = simID
        self.number = number
        self.body = body
    }

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case messageID = "message_id"
        case slotID = "slot_id"
        case simID = "sim_id"
        case number
        case body
    }

    func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        try values.encode(version, forKey: .version)
        try values.encode(operation, forKey: .operation)
        try values.encode(messageID, forKey: .messageID)
        try values.encodeIfPresent(slotID, forKey: .slotID)
        try values.encodeIfPresent(simID, forKey: .simID)
        try values.encode(number, forKey: .number)
        try values.encode(body, forKey: .body)
    }
}

struct IncomingMessagePayload: Codable, Equatable, Sendable {
    let version: Int
    let messageID: String
    let deviceUUID: UUID
    let slotID: UUID?
    let simID: UUID?
    let numericID: Int
    let remoteNumber: String
    let body: String
    let receivedAt: Date
    let segments: Int
    let installationID: UUID

    init(
        version: Int,
        messageID: String,
        deviceUUID: UUID,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        numericID: Int,
        remoteNumber: String,
        body: String,
        receivedAt: Date,
        segments: Int,
        installationID: UUID
    ) {
        self.version = version
        self.messageID = messageID
        self.deviceUUID = deviceUUID
        self.slotID = slotID
        self.simID = simID
        self.numericID = numericID
        self.remoteNumber = remoteNumber
        self.body = body
        self.receivedAt = receivedAt
        self.segments = segments
        self.installationID = installationID
    }

    enum CodingKeys: String, CodingKey {
        case version
        case messageID = "message_id"
        case deviceUUID = "device_uuid"
        case slotID = "slot_id"
        case simID = "sim_id"
        case numericID = "numeric_id"
        case remoteNumber = "remote_number"
        case body
        case receivedAt = "received_at"
        case segments
        case installationID = "installation_id"
    }
}

enum EncryptedMessageEventCodec {
    static func open(
        _ envelope: EncryptedEventEnvelope,
        installationIdentity: InstallationIdentity,
        deviceCommandPublicKey: Data,
        deviceVerificationKey: Data
    ) throws -> IncomingMessagePayload {
        guard envelope.type == "message.received",
              envelope.keyEpoch == 1,
              envelope.nonce.count == 12,
              envelope.ciphertext.count > 16,
              envelope.signature.count == 64,
              deviceCommandPublicKey.count == 32,
              deviceVerificationKey.count == 32 else {
            throw LiveMessageTransportError.invalidResponse
        }
        let signingKey = try Curve25519.Signing.PublicKey(
            rawRepresentation: deviceVerificationKey
        )
        guard signingKey.isValidSignature(
            envelope.signature,
            for: EncryptedCallEventCodec.signedData(envelope)
        ) else {
            throw LiveMessageTransportError.invalidResponse
        }
        let installationKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: installationIdentity.keyAgreementPrivateKey
        )
        let deviceKey = try Curve25519.KeyAgreement.PublicKey(
            rawRepresentation: deviceCommandPublicKey
        )
        let sharedSecret = try installationKey.sharedSecretFromKeyAgreement(with: deviceKey)
        let key = sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data(
                "\(envelope.id.uuidString.lowercased())\0\(envelope.deviceUUID.uuidString.lowercased())".utf8
            ),
            sharedInfo: Data("sim-deck-message-event-v1".utf8),
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
            authenticating: EncryptedCallEventCodec.associatedData(envelope)
        )
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let payload = try decoder.decode(IncomingMessagePayload.self, from: plaintext)
        guard payload.version == 1,
              payload.deviceUUID == envelope.deviceUUID,
              payload.installationID == installationIdentity.id,
              payload.numericID >= 1,
              payload.numericID <= 99,
              payload.segments >= 1,
              payload.segments <= 255,
              payload.messageID.count == 64,
              payload.messageID.allSatisfy({ $0.isHexDigit }),
              !payload.remoteNumber.isEmpty,
              !payload.body.isEmpty else {
            throw LiveMessageTransportError.invalidResponse
        }
        return payload
    }
}
