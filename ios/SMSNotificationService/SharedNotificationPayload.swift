import CryptoKit
import Foundation
import Security

struct NotificationInstallationIdentity: Decodable {
    let id: UUID
    let keyAgreementPrivateKey: Data
}

struct NotificationCachedDeviceKeys: Decodable {
    let verificationKey: Data
    let commandKey: Data
}

struct NotificationEventEnvelope: Decodable {
    let id: UUID
    let type: String
    let deviceUUID: UUID
    let recipientInstallationID: UUID?
    let occurredAt: Date
    let keyEpoch: Int
    let nonce: Data
    let ciphertext: Data
    let signature: Data

    enum CodingKeys: String, CodingKey {
        case id, type, occurredAt = "occurred_at", keyEpoch = "key_epoch", nonce, ciphertext, signature
        case deviceUUID = "device_uuid"
        case recipientInstallationID = "recipient_installation_id"
    }
}

struct NotificationMessagePayload: Decodable {
    let version: Int
    let deviceUUID: UUID
    let numericID: Int
    let remoteNumber: String
    let body: String
    let installationID: UUID

    enum CodingKeys: String, CodingKey {
        case version, numericID = "numeric_id", remoteNumber = "remote_number", body
        case deviceUUID = "device_uuid"
        case installationID = "installation_id"
    }
}

enum NotificationPayloadDecoder {
    static let group = "group.com.nowcent.simdeck"
    static let installationService = "com.orangeboychen.simdeck.installation"
    static let installationAccount = "identity-v1"
    static let keychainGroup = "97YACL9S6W.com.nowcent.simdeck.shared"
    static let deviceKeysKey = "sim-deck.notification.device-keys.v1"
    static let previewKey = "sim-deck.notifications.message-preview"
    static let otpKey = "simdeck.sms.otp"

    static func open(userInfo: [AnyHashable: Any]) -> (NotificationMessagePayload, Bool)? {
        guard let envelopeData = try? JSONSerialization.data(
            withJSONObject: userInfo.reduce(into: [String: Any]()) { result, entry in
                guard let key = entry.key as? String else { return }
                result[key] = entry.value
            }
        ),
        let envelope = try? decoder.decode(NotificationEventEnvelope.self, from: envelopeData),
        envelope.type == "message.received",
        let identityData = readIdentity(),
        let identity = try? decoder.decode(NotificationInstallationIdentity.self, from: identityData),
        envelope.recipientInstallationID == identity.id,
        let keys = cachedKeys()[envelope.deviceUUID.uuidString.lowercased()],
        let payload = try? decrypt(envelope: envelope, identity: identity, keys: keys),
        payload.version == 1,
        payload.deviceUUID == envelope.deviceUUID,
        payload.installationID == identity.id,
        !payload.remoteNumber.isEmpty,
        !payload.body.isEmpty else { return nil }
        let showPreview = (UserDefaults(suiteName: group) ?? .standard).bool(forKey: previewKey)
        return (payload, showPreview)
    }

    private static var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }

    private static func readIdentity() -> Data? {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: installationService,
            kSecAttrAccount as String: installationAccount,
            kSecAttrSynchronizable as String: false,
            kSecAttrAccessGroup as String: keychainGroup,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess else { return nil }
        return result as? Data
    }

    private static func cachedKeys() -> [String: NotificationCachedDeviceKeys] {
        let defaults = UserDefaults(suiteName: group) ?? .standard
        guard let data = defaults.data(forKey: deviceKeysKey),
              let encoded = try? decoder.decode([String: NotificationCachedDeviceKeys].self, from: data) else {
            return [:]
        }
        return encoded
    }

    private static func decrypt(
        envelope: NotificationEventEnvelope,
        identity: NotificationInstallationIdentity,
        keys: NotificationCachedDeviceKeys
    ) throws -> NotificationMessagePayload {
        let signingKey = try Curve25519.Signing.PublicKey(rawRepresentation: keys.verificationKey)
        guard signingKey.isValidSignature(envelope.signature, for: signedData(envelope)) else {
            throw NSError(domain: "SimDeckNotification", code: 1)
        }
        let installationKey = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: identity.keyAgreementPrivateKey)
        let deviceKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: keys.commandKey)
        let sharedSecret = try installationKey.sharedSecretFromKeyAgreement(with: deviceKey)
        let key = sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(envelope.id.uuidString.lowercased())\0\(envelope.deviceUUID.uuidString.lowercased())".utf8),
            sharedInfo: Data("sim-deck-message-event-v1".utf8),
            outputByteCount: 32
        )
        let box = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: envelope.nonce),
            ciphertext: Data(envelope.ciphertext.dropLast(16)),
            tag: Data(envelope.ciphertext.suffix(16))
        )
        return try decoder.decode(
            NotificationMessagePayload.self,
            from: AES.GCM.open(box, using: key, authenticating: associatedData(envelope))
        )
    }

    private static func associatedData(_ envelope: NotificationEventEnvelope) -> Data {
        var data = Data()
        data.appendLengthPrefixed(Data(envelope.id.uuidString.lowercased().utf8))
        data.appendLengthPrefixed(Data(envelope.type.utf8))
        data.appendLengthPrefixed(Data(envelope.deviceUUID.uuidString.lowercased().utf8))
        if let recipient = envelope.recipientInstallationID {
            data.appendLengthPrefixed(Data(recipient.uuidString.lowercased().utf8))
        }
        let nanoseconds = Int64((envelope.occurredAt.timeIntervalSince1970 * 1_000_000_000).rounded())
        data.appendBigEndian(UInt64(bitPattern: nanoseconds))
        data.appendBigEndian(UInt64(envelope.keyEpoch))
        return data
    }

    private static func signedData(_ envelope: NotificationEventEnvelope) -> Data {
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
        var encoded = value.bigEndian
        Swift.withUnsafeBytes(of: &encoded) { append(contentsOf: $0) }
    }
}
