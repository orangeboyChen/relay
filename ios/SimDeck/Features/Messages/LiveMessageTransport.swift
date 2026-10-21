// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation
import OSLog

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

struct MessageSyncBatch: Equatable, Sendable {
    let deliveries: [IncomingMessageDelivery]
    let detectedSIMProfiles: [DetectedSIMProfile]
    let nextSequence: Int64

    init(
        deliveries: [IncomingMessageDelivery],
        detectedSIMProfiles: [DetectedSIMProfile] = [],
        nextSequence: Int64
    ) {
        self.deliveries = deliveries
        self.detectedSIMProfiles = detectedSIMProfiles
        self.nextSequence = nextSequence
    }
}

struct DetectedSIMProfile: Equatable, Sendable {
    let lineID: UUID
    let lineName: String
    let phoneNumber: String
}

@MainActor
protocol MessageTransport: AnyObject {
    var currentSequence: Int64 { get }
    func send(messageID: UUID, lineID: UUID, remoteNumber: String, body: String) async throws
    func sendMMS(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String,
        attachment: MessageImageAttachment
    ) async throws
    func synchronize(lines: [DeviceLineIdentity]) async throws -> MessageSyncBatch
    func acknowledge(sequence: Int64)
    func updateLineRoutes(_ lines: [DeviceLineIdentity])
}

extension MessageTransport {
    var currentSequence: Int64 { 0 }
    func sendMMS(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String,
        attachment: MessageImageAttachment
    ) async throws {
        throw LiveMessageTransportError.modemRejected("mms_unavailable")
    }
    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {}
    func acknowledge(sequence: Int64) {}
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
    private static let eventPageSize = 100
    private static let logger = Logger(
        subsystem: Bundle.main.bundleIdentifier ?? "SimDeck",
        category: "EncryptedMessages"
    )

    private let api: any EncryptedCallCommandAPI
    private let commandSigner: InstallationCommandSigner
    private let cursorStore: any MessageCursorStoring
    private let now: () -> Date
    private let makeCommandID: () -> UUID
    private let makeSenderKey: () -> Curve25519.KeyAgreement.PrivateKey
    private let waitBeforePollingAgain: () async throws -> Void
    private var lineRoutes: [UUID: DeviceLineIdentity] = [:]

    var currentSequence: Int64 { cursorStore.sequence }

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

    func send(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String
    ) async throws {
        try await sendCommand(
            messageID: messageID,
            lineID: lineID,
            remoteNumber: remoteNumber,
            body: body,
            attachment: nil
        )
    }

    func sendMMS(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String,
        attachment: MessageImageAttachment
    ) async throws {
        guard let data = attachment.data,
              let mimeType = attachment.mimeType,
              let filename = attachment.filename else {
            throw LiveMessageTransportError.invalidResponse
        }
        try await sendCommand(
            messageID: messageID,
            lineID: lineID,
            remoteNumber: remoteNumber,
            body: body,
            attachment: MessageCommandAttachment(
                filename: filename,
                mimeType: mimeType,
                data: data
            )
        )
    }

    private func sendCommand(
        messageID: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String,
        attachment: MessageCommandAttachment?
    ) async throws {
        guard let installationIdentity = commandSigner.identity else {
            throw LiveMessageTransportError.missingInstallationIdentity
        }
        let line = lineRoutes[lineID] ?? DeviceLineIdentity(
            id: lineID,
            numericID: 1,
            deviceName: "",
            lineName: "",
            phoneNumber: "",
            isOnline: true
        )
        let target = try await api.commandTarget(for: line)
        let commandID = makeCommandID()
        let payload = MessageCommandPayload(
            version: 1,
            operation: "message.send",
            messageID: messageID.uuidString.lowercased(),
            slotID: target.slotID,
            simID: target.simID,
            number: remoteNumber,
            body: body,
            attachment: attachment
        )
        let sealed: SealedCallCommand
        do {
            sealed = try EncryptedCallCommandCodec.seal(
                payload: payload,
                recipientPublicKey: target.publicKey,
                senderPrivateKey: makeSenderKey(),
                nonce: AES.GCM.Nonce(),
                commandID: commandID,
                deviceID: target.deviceID,
                createdAt: Date(timeIntervalSince1970: floor(now().timeIntervalSince1970)),
                installationIdentity: installationIdentity
            )
        } catch is CryptoKitError {
            throw LiveMessageTransportError.invalidDeviceKey
        }
        try await api.enqueueCommand(deviceID: target.deviceID, command: sealed.request)

        let maximumAttempts = attachment == nil ? 60 : 300
        for attempt in 0..<maximumAttempts {
            let status = try await api.commandStatus(deviceID: target.deviceID, commandID: commandID)
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
                        deviceID: target.deviceID
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
            if attempt + 1 < maximumAttempts {
                try await waitBeforePollingAgain()
            }
        }
        throw LiveMessageTransportError.commandTimedOut
    }

    func synchronize(lines: [DeviceLineIdentity]) async throws -> MessageSyncBatch {
        guard let installationIdentity = commandSigner.identity else {
            throw LiveMessageTransportError.missingInstallationIdentity
        }
        let linesByDevice = Dictionary(grouping: lines, by: { $0.deviceID })
        var verificationKeys: [UUID: Data] = [:]
        var deliveries: [IncomingMessageDelivery] = []
        var detectedSIMProfiles: [DetectedSIMProfile] = []
        var nextSequence = cursorStore.sequence

        while true {
            let page = try await api.encryptedEvents(afterSequence: nextSequence)
            for envelope in page.events where envelope.type == "message.received" {
                guard envelope.recipientInstallationID == installationIdentity.id else {
                    continue
                }
                guard let candidates = linesByDevice[envelope.deviceUUID],
                    let commandPublicKey = candidates.first?.commandPublicKey
                else {
                    throw LiveMessageTransportError.invalidResponse
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
                let payload: IncomingMessagePayload
                do {
                    payload = try EncryptedMessageEventCodec.open(
                        envelope,
                        installationIdentity: installationIdentity,
                        deviceCommandPublicKey: commandPublicKey,
                        deviceVerificationKey: verificationKey
                    )
                } catch {
                    Self.logEventFailure(envelope, stage: "authenticate-or-decrypt", error: error)
                    throw error
                }
                let matchingLines: [DeviceLineIdentity]
                if let payloadSlotID = payload.slotID {
                    matchingLines = candidates.filter { $0.slotID == payloadSlotID }
                } else if let payloadSIMID = payload.simID {
                    matchingLines = candidates.filter { $0.simID == payloadSIMID }
                } else {
                    matchingLines = candidates.count == 1 ? candidates : []
                }
                guard matchingLines.count == 1 else {
                    throw LiveMessageTransportError.invalidResponse
                }
                deliveries.append(
                    IncomingMessageDelivery(
                        id: envelope.id,
                        sourceID: payload.messageID,
                        lineID: matchingLines[0].id,
                        remoteNumber: payload.remoteNumber,
                        body: payload.body,
                        receivedAt: payload.receivedAt
                    )
                )
            }
            for envelope in page.events where envelope.type == "sim.profile" {
                guard envelope.recipientInstallationID == installationIdentity.id else {
                    continue
                }
                guard let candidates = linesByDevice[envelope.deviceUUID],
                    let commandPublicKey = candidates.first?.commandPublicKey
                else {
                    throw LiveMessageTransportError.invalidResponse
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
                let payload: IncomingSIMProfilePayload
                do {
                    payload = try EncryptedSIMProfileEventCodec.open(
                        envelope,
                        installationIdentity: installationIdentity,
                        deviceCommandPublicKey: commandPublicKey,
                        deviceVerificationKey: verificationKey
                    )
                } catch {
                    Self.logEventFailure(envelope, stage: "authenticate-or-decrypt", error: error)
                    throw error
                }
                let matchingLines = candidates.filter { line in
                    if let slotID = payload.slotID, line.slotID != slotID {
                        return false
                    }
                    if let simID = payload.simID, line.simID != simID {
                        return false
                    }
                    return payload.slotID != nil || payload.simID != nil || candidates.count == 1
                }
                guard matchingLines.count == 1 else {
                    throw LiveMessageTransportError.invalidResponse
                }
                detectedSIMProfiles.append(
                    DetectedSIMProfile(
                        lineID: matchingLines[0].id,
                        lineName: payload.lineName,
                        phoneNumber: payload.phoneNumber
                    )
                )
            }
            guard page.nextSequence > nextSequence else { break }
            nextSequence = page.nextSequence
            if page.events.count < Self.eventPageSize { break }
        }
        return MessageSyncBatch(
            deliveries: deliveries,
            detectedSIMProfiles: detectedSIMProfiles,
            nextSequence: nextSequence
        )
    }

    func acknowledge(sequence: Int64) {
        cursorStore.sequence = max(cursorStore.sequence, sequence)
    }

    private static func logEventFailure(
        _ envelope: EncryptedEventEnvelope,
        stage: String,
        error: any Error
    ) {
        logger.error(
            "Encrypted event failed; sequence=\(envelope.sequence, privacy: .public), type=\(envelope.type, privacy: .public), stage=\(stage, privacy: .public), error=\(String(describing: error), privacy: .private(mask: .hash))"
        )
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
    let attachment: MessageCommandAttachment?

    init(
        version: Int,
        operation: String,
        messageID: String,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        number: String,
        body: String,
        attachment: MessageCommandAttachment? = nil
    ) {
        self.version = version
        self.operation = operation
        self.messageID = messageID
        self.slotID = slotID
        self.simID = simID
        self.number = number
        self.body = body
        self.attachment = attachment
    }

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case messageID = "message_id"
        case slotID = "slot_id"
        case simID = "sim_id"
        case number
        case body
        case attachment
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
        try values.encodeIfPresent(attachment, forKey: .attachment)
    }
}

struct MessageCommandAttachment: Codable, Equatable, Sendable {
    let filename: String
    let mimeType: String
    let data: Data

    enum CodingKeys: String, CodingKey {
        case filename
        case mimeType = "mime_type"
        case data
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

struct IncomingSIMProfilePayload: Codable, Equatable, Sendable {
    let version: Int
    let deviceUUID: UUID
    let slotID: UUID?
    let simID: UUID?
    let numericID: Int
    let phoneNumber: String
    let lineName: String
    let installationID: UUID

    enum CodingKeys: String, CodingKey {
        case version
        case deviceUUID = "device_uuid"
        case slotID = "slot_id"
        case simID = "sim_id"
        case numericID = "numeric_id"
        case phoneNumber = "phone_number"
        case lineName = "line_name"
        case installationID = "installation_id"
    }
}

enum EncryptedSIMProfileEventCodec {
    static func open(
        _ envelope: EncryptedEventEnvelope,
        installationIdentity: InstallationIdentity,
        deviceCommandPublicKey: Data,
        deviceVerificationKey: Data
    ) throws -> IncomingSIMProfilePayload {
        guard envelope.type == "sim.profile",
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
            sharedInfo: Data("sim-deck-sim-profile-event-v1".utf8),
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
        let payload = try JSONDecoder().decode(IncomingSIMProfilePayload.self, from: plaintext)
        let normalizedNumber = DialString.normalized(payload.phoneNumber)
        let normalizedLineName = payload.lineName.trimmingCharacters(
            in: .whitespacesAndNewlines
        )
        guard payload.version == 1,
              payload.deviceUUID == envelope.deviceUUID,
              payload.installationID == installationIdentity.id,
              payload.numericID >= 1,
              payload.numericID <= 99,
              payload.lineName.count <= 64,
              payload.phoneNumber.isEmpty || DialString.isValid(normalizedNumber),
              !normalizedNumber.isEmpty || !normalizedLineName.isEmpty else {
            throw LiveMessageTransportError.invalidResponse
        }
        return payload
    }
}
