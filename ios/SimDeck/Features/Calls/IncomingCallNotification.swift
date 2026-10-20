// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

enum IncomingCallNotificationError: Error, Equatable {
    case invalidEnvelope
    case invalidSignature
    case decryptionFailed
}

struct IncomingCallRecipientEnvelope: Decodable, Equatable, Sendable {
    let installationID: UUID
    let senderPublicKey: Data
    let nonce: Data
    let ciphertext: Data
    let signature: Data

    enum CodingKeys: String, CodingKey {
        case installationID = "installation_id"
        case senderPublicKey = "sender_public_key"
        case nonce
        case ciphertext
        case signature
    }
}

struct IncomingCallDelivery: Decodable, Equatable, Sendable {
    let notificationID: UUID
    let type: String
    let deviceUUID: UUID
    let occurredAt: Date
    let keyEpoch: Int
    let recipient: IncomingCallRecipientEnvelope

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case type
        case deviceUUID = "device_uuid"
        case occurredAt = "occurred_at"
        case keyEpoch = "key_epoch"
        case recipient
    }
}

struct IncomingCallPayload: Decodable, Equatable, Sendable {
    let version: Int
    let callID: UUID
    let deviceUUID: UUID
    let slotID: UUID?
    let simID: UUID?
    let numericID: Int
    let remoteNumber: String
    let state: String
    let callSecret: Data
    let notificationID: UUID

    enum CodingKeys: String, CodingKey {
        case version
        case callID = "call_id"
        case deviceUUID = "device_uuid"
        case slotID = "slot_id"
        case simID = "sim_id"
        case numericID = "numeric_id"
        case remoteNumber = "remote_number"
        case state
        case callSecret = "call_secret"
        case notificationID = "notification_id"
    }
}

struct OpenedIncomingCall: Equatable, Sendable {
    let delivery: IncomingCallDelivery
    let payload: IncomingCallPayload
}

enum IncomingCallLineResolver {
    static func resolve(
        payload: IncomingCallPayload,
        lines: [DeviceLineIdentity],
        cachedDeviceKeys: CachedDeviceKeys?
    ) -> DeviceLineIdentity? {
        let matches: [DeviceLineIdentity]
        if let slotID = payload.slotID {
            matches = lines.filter { $0.deviceID == payload.deviceUUID && $0.slotID == slotID }
        } else if let simID = payload.simID {
            matches = lines.filter { $0.deviceID == payload.deviceUUID && $0.simID == simID }
        } else {
            matches = lines.filter { $0.deviceID == payload.deviceUUID }
        }
        if matches.count == 1 {
            return matches[0]
        }

        guard let lineID = payload.slotID ?? payload.simID,
              let cachedDeviceKeys,
              cachedDeviceKeys.commandKey.count == 32 else {
            return nil
        }
        return DeviceLineIdentity(
            id: lineID,
            deviceID: payload.deviceUUID,
            slotID: payload.slotID,
            simID: payload.simID,
            numericID: payload.numericID,
            deviceName: "",
            lineName: "",
            phoneNumber: "",
            isOnline: true,
            commandPublicKey: cachedDeviceKeys.commandKey
        )
    }
}

enum IncomingCallPushDelivery: Equatable, Sendable {
    case incoming(IncomingCallDelivery)
    case answeredElsewhere(notificationID: UUID)

    static func decode(_ data: Data) throws -> IncomingCallPushDelivery {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        if let delivery = try? decoder.decode(IncomingCallDelivery.self, from: data),
           delivery.type == "call.incoming" {
            return .incoming(delivery)
        }
        let claim: ClaimDelivery
        do {
            claim = try decoder.decode(ClaimDelivery.self, from: data)
        } catch {
            throw IncomingCallNotificationError.invalidEnvelope
        }
        guard claim.reason == "answered_elsewhere" else {
            throw IncomingCallNotificationError.invalidEnvelope
        }
        return .answeredElsewhere(notificationID: claim.notificationID)
    }

    private struct ClaimDelivery: Decodable {
        let notificationID: UUID
        let reason: String

        enum CodingKeys: String, CodingKey {
            case notificationID = "notification_id"
            case reason
        }
    }
}

enum IncomingCallNotificationCodec {
    private static let context = "sim-deck-incoming-call-v1"

    static func open(
        _ data: Data,
        installationIdentity: InstallationIdentity,
        deviceVerificationKey: Data
    ) throws -> OpenedIncomingCall {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let delivery: IncomingCallDelivery
        do {
            delivery = try decoder.decode(IncomingCallDelivery.self, from: data)
        } catch {
            throw IncomingCallNotificationError.invalidEnvelope
        }
        let recipient = delivery.recipient
        guard delivery.type == "call.incoming",
              delivery.keyEpoch == 1,
              recipient.installationID == installationIdentity.id,
              recipient.senderPublicKey.count == 32,
              recipient.nonce.count == 12,
              recipient.ciphertext.count >= 16,
              recipient.signature.count == 64,
              deviceVerificationKey.count == 32 else {
            throw IncomingCallNotificationError.invalidEnvelope
        }

        let verificationKey: Curve25519.Signing.PublicKey
        do {
            verificationKey = try Curve25519.Signing.PublicKey(
                rawRepresentation: deviceVerificationKey
            )
        } catch {
            throw IncomingCallNotificationError.invalidEnvelope
        }
        guard verificationKey.isValidSignature(
            recipient.signature,
            for: signedData(delivery)
        ) else {
            throw IncomingCallNotificationError.invalidSignature
        }

        do {
            let installationKey = try Curve25519.KeyAgreement.PrivateKey(
                rawRepresentation: installationIdentity.keyAgreementPrivateKey
            )
            let senderKey = try Curve25519.KeyAgreement.PublicKey(
                rawRepresentation: recipient.senderPublicKey
            )
            let sharedSecret = try installationKey.sharedSecretFromKeyAgreement(with: senderKey)
            let key = sharedSecret.hkdfDerivedSymmetricKey(
                using: SHA256.self,
                salt: Data(
                    "\(delivery.notificationID.uuidString.lowercased())\0\(recipient.installationID.uuidString.lowercased())".utf8
                ),
                sharedInfo: Data(context.utf8),
                outputByteCount: 32
            )
            let box = try AES.GCM.SealedBox(
                nonce: AES.GCM.Nonce(data: recipient.nonce),
                ciphertext: Data(recipient.ciphertext.dropLast(16)),
                tag: Data(recipient.ciphertext.suffix(16))
            )
            let plaintext = try AES.GCM.open(
                box,
                using: key,
                authenticating: associatedData(delivery)
            )
            let payload = try JSONDecoder().decode(IncomingCallPayload.self, from: plaintext)
            guard payload.version == 1,
                  payload.notificationID == delivery.notificationID,
                  payload.deviceUUID == delivery.deviceUUID,
                  (1...99).contains(payload.numericID),
                  payload.state == "ringing" || payload.state == "waiting",
                  validPhoneNumber(payload.remoteNumber),
                  payload.callSecret.count == 32 else {
                throw IncomingCallNotificationError.invalidEnvelope
            }
            return OpenedIncomingCall(delivery: delivery, payload: payload)
        } catch let error as IncomingCallNotificationError {
            throw error
        } catch {
            throw IncomingCallNotificationError.decryptionFailed
        }
    }

    private static func validPhoneNumber(_ value: String) -> Bool {
        let characters = Array(value)
        guard (1...20).contains(characters.count) else { return false }
        return characters.enumerated().allSatisfy { index, character in
            character.isNumber || (index == 0 && character == "+")
        }
    }

    private static func associatedData(_ delivery: IncomingCallDelivery) -> Data {
        var data = Data()
        appendLengthPrefixed(Data(context.utf8), to: &data)
        appendLengthPrefixed(
            Data(delivery.notificationID.uuidString.lowercased().utf8),
            to: &data
        )
        appendLengthPrefixed(Data(delivery.type.utf8), to: &data)
        appendLengthPrefixed(
            Data(delivery.deviceUUID.uuidString.lowercased().utf8),
            to: &data
        )
        let nanoseconds = signedTimestampNanoseconds(delivery.occurredAt)
        appendBigEndian(UInt64(bitPattern: nanoseconds), to: &data)
        appendBigEndian(UInt64(delivery.keyEpoch), to: &data)
        appendLengthPrefixed(
            Data(delivery.recipient.installationID.uuidString.lowercased().utf8),
            to: &data
        )
        appendLengthPrefixed(delivery.recipient.senderPublicKey, to: &data)
        return data
    }

    private static func signedData(_ delivery: IncomingCallDelivery) -> Data {
        var data = Data()
        appendLengthPrefixed(
            Data(delivery.notificationID.uuidString.lowercased().utf8),
            to: &data
        )
        appendLengthPrefixed(Data(delivery.type.utf8), to: &data)
        appendLengthPrefixed(
            Data(delivery.deviceUUID.uuidString.lowercased().utf8),
            to: &data
        )
        let nanoseconds = signedTimestampNanoseconds(delivery.occurredAt)
        appendBigEndian(UInt64(bitPattern: nanoseconds), to: &data)
        appendBigEndian(UInt64(delivery.keyEpoch), to: &data)
        appendLengthPrefixed(
            Data(delivery.recipient.installationID.uuidString.lowercased().utf8),
            to: &data
        )
        appendLengthPrefixed(delivery.recipient.senderPublicKey, to: &data)
        appendLengthPrefixed(delivery.recipient.nonce, to: &data)
        appendLengthPrefixed(delivery.recipient.ciphertext, to: &data)
        return data
    }

    private static func appendLengthPrefixed(_ value: Data, to data: inout Data) {
        appendBigEndian(UInt32(value.count), to: &data)
        data.append(value)
    }

    private static func signedTimestampNanoseconds(_ date: Date) -> Int64 {
        let milliseconds = Int64((date.timeIntervalSince1970 * 1_000).rounded())
        return milliseconds * 1_000_000
    }

    private static func appendBigEndian<T: FixedWidthInteger>(_ value: T, to data: inout Data) {
        var bigEndian = value.bigEndian
        Swift.withUnsafeBytes(of: &bigEndian) { bytes in
            data.append(contentsOf: bytes)
        }
    }
}
