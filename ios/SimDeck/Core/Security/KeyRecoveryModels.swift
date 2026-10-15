import CryptoKit
import Foundation
import Security

enum KeyRecoveryError: Error, Equatable {
    case invalidVaultKey
    case invalidInstallationIdentity
    case keychain(OSStatus)
}

struct VaultKey: Equatable, Sendable {
    static let length = 32

    let data: Data

    init(data: Data) throws {
        guard data.count == Self.length else {
            throw KeyRecoveryError.invalidVaultKey
        }
        self.data = data
    }

    static func random() -> VaultKey {
        let key = SymmetricKey(size: .bits256)
        let data = key.withUnsafeBytes { Data($0) }
        return try! VaultKey(data: data)
    }
}

struct InstallationIdentity: Codable, Equatable, Sendable {
    private enum CodingKeys: String, CodingKey {
        case id
        case signingPrivateKey
        case pendingRevocationIDs
    }

    let id: UUID
    let signingPrivateKey: Data
    var pendingRevocationIDs: [UUID]

    init(id: UUID, signingPrivateKey: Data, pendingRevocationIDs: [UUID]) {
        self.id = id
        self.signingPrivateKey = signingPrivateKey
        self.pendingRevocationIDs = pendingRevocationIDs
    }

    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UUID.self, forKey: .id)
        signingPrivateKey = try container.decode(Data.self, forKey: .signingPrivateKey)
        pendingRevocationIDs = try container.decodeIfPresent([UUID].self, forKey: .pendingRevocationIDs) ?? []
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(signingPrivateKey, forKey: .signingPrivateKey)
        try container.encode(pendingRevocationIDs, forKey: .pendingRevocationIDs)
    }

    static func generate(pendingRevocationIDs: [UUID] = []) -> InstallationIdentity {
        let privateKey = Curve25519.Signing.PrivateKey()
        return InstallationIdentity(
            id: UUID(),
            signingPrivateKey: privateKey.rawRepresentation,
            pendingRevocationIDs: pendingRevocationIDs
        )
    }

    func signingPublicKey() throws -> Data {
        do {
            return try Curve25519.Signing.PrivateKey(rawRepresentation: signingPrivateKey)
                .publicKey.rawRepresentation
        } catch {
            throw KeyRecoveryError.invalidInstallationIdentity
        }
    }
}

struct InstallationBootstrapResult: Equatable, Sendable {
    let identity: InstallationIdentity
    let recoveredVaultKey: VaultKey?
    let pendingRevocationIDs: [UUID]
}

protocol InstallationBootstrapping: Sendable {
    func bootstrap() throws -> InstallationBootstrapResult
    func saveVaultKey(_ key: VaultKey) throws
    func markInstallationRevoked(_ id: UUID) throws
}

protocol SecureValueStoring: Sendable {
    func read() throws -> Data?
    func write(_ value: Data) throws
    func delete() throws
}

protocol InstallMarkerStoring: Sendable {
    func isInstalled() -> Bool
    func markInstalled()
}
