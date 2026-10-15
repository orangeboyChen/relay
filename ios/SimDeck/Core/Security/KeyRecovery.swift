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

struct InstallationBootstrapper: InstallationBootstrapping {
    private let identityStore: any SecureValueStoring
    private let vaultStore: any SecureValueStoring
    private let markerStore: any InstallMarkerStoring

    init(
        identityStore: any SecureValueStoring,
        vaultStore: any SecureValueStoring,
        markerStore: any InstallMarkerStoring
    ) {
        self.identityStore = identityStore
        self.vaultStore = vaultStore
        self.markerStore = markerStore
    }

    func bootstrap() throws -> InstallationBootstrapResult {
        let previousIdentity = try loadIdentity()
        var identity = previousIdentity

        if !markerStore.isInstalled(), let previousIdentity {
            let pendingRevocationIDs = previousIdentity.pendingRevocationIDs + [previousIdentity.id]
            try identityStore.delete()
            let generated = InstallationIdentity.generate(pendingRevocationIDs: pendingRevocationIDs)
            try saveIdentity(generated)
            identity = generated
        }
        if identity == nil {
            let generated = InstallationIdentity.generate()
            try saveIdentity(generated)
            identity = generated
        }
        markerStore.markInstalled()

        guard let identity else {
            throw KeyRecoveryError.invalidInstallationIdentity
        }
        let recoveredVaultKey = try vaultStore.read().map(VaultKey.init(data:))
        return InstallationBootstrapResult(
            identity: identity,
            recoveredVaultKey: recoveredVaultKey,
            pendingRevocationIDs: identity.pendingRevocationIDs
        )
    }

    func saveVaultKey(_ key: VaultKey) throws {
        try vaultStore.write(key.data)
    }

    func markInstallationRevoked(_ id: UUID) throws {
        guard var identity = try loadIdentity() else {
            throw KeyRecoveryError.invalidInstallationIdentity
        }
        identity.pendingRevocationIDs.removeAll { $0 == id }
        try saveIdentity(identity)
    }

    private func loadIdentity() throws -> InstallationIdentity? {
        guard let data = try identityStore.read() else {
            return nil
        }
        do {
            let identity = try JSONDecoder().decode(InstallationIdentity.self, from: data)
            _ = try identity.signingPublicKey()
            return identity
        } catch let error as KeyRecoveryError {
            throw error
        } catch {
            throw KeyRecoveryError.invalidInstallationIdentity
        }
    }

    private func saveIdentity(_ identity: InstallationIdentity) throws {
        try identityStore.write(JSONEncoder().encode(identity))
    }
}

extension InstallationBootstrapper {
    static func live() -> InstallationBootstrapper {
        InstallationBootstrapper(
            identityStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.installation",
                account: "identity-v1",
                synchronizable: false,
                accessibility: kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String
            ),
            vaultStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.recovery",
                account: "vault-v1",
                synchronizable: true,
                accessibility: kSecAttrAccessibleWhenUnlocked as String
            ),
            markerStore: UserDefaultsInstallMarkerStore()
        )
    }
}

struct KeychainValueStore: SecureValueStoring {
    let service: String
    let account: String
    let synchronizable: Bool
    let accessibility: String

    func read() throws -> Data? {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw KeyRecoveryError.keychain(status)
        }
        return data
    }

    func write(_ value: Data) throws {
        var attributes = baseQuery
        attributes[kSecValueData as String] = value
        attributes[kSecAttrAccessible as String] = accessibility
        let status = SecItemAdd(attributes as CFDictionary, nil)
        if status == errSecDuplicateItem {
            let updateStatus = SecItemUpdate(
                baseQuery as CFDictionary,
                [
                    kSecValueData as String: value,
                    kSecAttrAccessible as String: accessibility,
                ] as CFDictionary
            )
            guard updateStatus == errSecSuccess else {
                throw KeyRecoveryError.keychain(updateStatus)
            }
            return
        }
        guard status == errSecSuccess else {
            throw KeyRecoveryError.keychain(status)
        }
    }

    func delete() throws {
        let status = SecItemDelete(baseQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeyRecoveryError.keychain(status)
        }
    }

    private var baseQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrSynchronizable as String: synchronizable,
        ]
    }
}

struct UserDefaultsInstallMarkerStore: InstallMarkerStoring {
    private let key = "sim-deck.installation-marker.v1"

    func isInstalled() -> Bool {
        UserDefaults.standard.bool(forKey: key)
    }

    func markInstalled() {
        UserDefaults.standard.set(true, forKey: key)
    }
}

struct PreviewInstallationBootstrapper: InstallationBootstrapping {
    func bootstrap() throws -> InstallationBootstrapResult {
        InstallationBootstrapResult(
            identity: InstallationIdentity(
                id: UUID(uuidString: "30000000-0000-0000-0000-000000000001")!,
                signingPrivateKey: Data(repeating: 1, count: 32),
                pendingRevocationIDs: []
            ),
            recoveredVaultKey: try VaultKey(data: Data(repeating: 2, count: VaultKey.length)),
            pendingRevocationIDs: []
        )
    }

    func saveVaultKey(_: VaultKey) throws { }

    func markInstallationRevoked(_: UUID) throws { }
}
