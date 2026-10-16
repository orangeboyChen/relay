import Foundation
import Security

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
        let wasInstalled = markerStore.isInstalled()
        var previousIdentity = try loadIdentity()
        let hadPersistedIdentity = previousIdentity != nil
        var recoveredVaultKey = try vaultStore.read().map(VaultKey.init(data:))

        if let loadedIdentity = previousIdentity,
           loadedIdentity.keyAgreementPrivateKey.isEmpty {
            let upgradedIdentity = loadedIdentity.addingKeyAgreementIdentity()
            try saveIdentity(upgradedIdentity)
            previousIdentity = upgradedIdentity
        }
        var identity = previousIdentity

        if !wasInstalled, let previousIdentity {
            let pendingRevocationIDs = previousIdentity.pendingRevocationIDs + [previousIdentity.id]
            try identityStore.delete()
            let generated = InstallationIdentity.generate(pendingRevocationIDs: pendingRevocationIDs)
            try saveIdentity(generated)
            identity = generated
        }
        if identity == nil {
            let generated = InstallationIdentity.generate()
            if recoveredVaultKey == nil, !wasInstalled, !hadPersistedIdentity {
                let generatedVaultKey = VaultKey.random()
                try vaultStore.write(generatedVaultKey.data)
                recoveredVaultKey = generatedVaultKey
            }
            try saveIdentity(generated)
            identity = generated
        }

        markerStore.markInstalled()

        guard let identity else {
            throw KeyRecoveryError.invalidInstallationIdentity
        }
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
            if !identity.keyAgreementPrivateKey.isEmpty {
                _ = try identity.keyAgreementPublicKey()
            }
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
