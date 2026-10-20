// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import Security

struct InstallationBootstrapper: InstallationBootstrapping {
    private let identityStore: any SecureValueStoring
    private let legacyIdentityStore: (any SecureValueStoring)?
    private let vaultStore: any SecureValueStoring
    private let markerStore: any InstallMarkerStoring

    init(
        identityStore: any SecureValueStoring,
        vaultStore: any SecureValueStoring,
        markerStore: any InstallMarkerStoring,
        legacyIdentityStore: (any SecureValueStoring)? = nil
    ) {
        self.identityStore = identityStore
        self.legacyIdentityStore = legacyIdentityStore
        self.vaultStore = vaultStore
        self.markerStore = markerStore
    }

    func bootstrap() throws -> InstallationBootstrapResult {
        try migrateLegacyIdentityIfNeeded()
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

    private func migrateLegacyIdentityIfNeeded() throws {
        guard try identityStore.read() == nil,
              let legacyIdentityStore,
              let legacyData = try legacyIdentityStore.read() else { return }
        try identityStore.write(legacyData)
        try legacyIdentityStore.delete()
    }
}

extension InstallationBootstrapper {
    static func live() -> InstallationBootstrapper {
        InstallationBootstrapper(
            identityStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.installation",
                account: "identity-v1",
                synchronizable: false,
                accessibility: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String,
                accessGroup: "97YACL9S6W.com.nowcent.simdeck.shared"
            ),
            vaultStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.recovery",
                account: "vault-v1",
                synchronizable: true,
                accessibility: kSecAttrAccessibleWhenUnlocked as String
            ),
            markerStore: UserDefaultsInstallMarkerStore(),
            legacyIdentityStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.installation",
                account: "identity-v1",
                synchronizable: false,
                accessibility: kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String
            )
        )
    }
}
