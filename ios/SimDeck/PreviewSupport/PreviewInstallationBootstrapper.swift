// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

#if DEBUG
import Foundation

struct PreviewInstallationBootstrapper: InstallationBootstrapping {
    func bootstrap() throws -> InstallationBootstrapResult {
        InstallationBootstrapResult(
            identity: InstallationIdentity(
                id: UUID(uuidString: "30000000-0000-0000-0000-000000000001")!,
                signingPrivateKey: Data(repeating: 1, count: 32),
                keyAgreementPrivateKey: Data(repeating: 2, count: 32),
                pendingRevocationIDs: []
            ),
            recoveredVaultKey: try VaultKey(data: Data(repeating: 2, count: VaultKey.length)),
            pendingRevocationIDs: []
        )
    }

    func saveVaultKey(_: VaultKey) throws { }

    func markInstallationRevoked(_: UUID) throws { }
}
#endif
