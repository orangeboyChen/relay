// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation
import XCTest
@testable import SimDeck

final class KeyRecoveryTests: XCTestCase {
    func testReinstallCreatesNewIdentityAndRecoversVaultKey() throws {
        let identityStore = MemorySecureValueStore()
        let vaultStore = MemorySecureValueStore()
        let markerStore = MemoryInstallMarkerStore()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: vaultStore,
            markerStore: markerStore
        )

        let first = try bootstrapper.bootstrap()
        let vaultKey = try XCTUnwrap(first.recoveredVaultKey)

        let relaunched = try bootstrapper.bootstrap()
        XCTAssertEqual(relaunched.identity.id, first.identity.id)
        XCTAssertEqual(relaunched.recoveredVaultKey, vaultKey)

        markerStore.removeMarkerToSimulateReinstall()
        let reinstalled = try bootstrapper.bootstrap()
        XCTAssertNotEqual(reinstalled.identity.id, first.identity.id)
        XCTAssertEqual(reinstalled.pendingRevocationIDs, [first.identity.id])
        XCTAssertEqual(reinstalled.recoveredVaultKey, vaultKey)

        let restartedBeforeRevocation = try bootstrapper.bootstrap()
        XCTAssertEqual(restartedBeforeRevocation.pendingRevocationIDs, [first.identity.id])

        try bootstrapper.markInstallationRevoked(first.identity.id)
        let restartedAfterRevocation = try bootstrapper.bootstrap()
        XCTAssertTrue(restartedAfterRevocation.pendingRevocationIDs.isEmpty)
    }

    func testFreshInstallGeneratesAndPersistsVaultKey() throws {
        let identityStore = MemorySecureValueStore()
        let vaultStore = MemorySecureValueStore()
        let markerStore = MemoryInstallMarkerStore()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: vaultStore,
            markerStore: markerStore
        )

        let result = try bootstrapper.bootstrap()
        let vaultKey = try XCTUnwrap(result.recoveredVaultKey)

        XCTAssertEqual(vaultKey.data.count, VaultKey.length)
        XCTAssertEqual(try vaultStore.read(), vaultKey.data)
        XCTAssertTrue(markerStore.isInstalled())
    }

    func testReinstallWithoutRecoverableVaultKeyRequiresApproval() throws {
        let identityStore = MemorySecureValueStore()
        let vaultStore = MemorySecureValueStore()
        let markerStore = MemoryInstallMarkerStore()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: vaultStore,
            markerStore: markerStore
        )

        let first = try bootstrapper.bootstrap()
        try vaultStore.delete()
        markerStore.removeMarkerToSimulateReinstall()

        let reinstalled = try bootstrapper.bootstrap()

        XCTAssertNotEqual(reinstalled.identity.id, first.identity.id)
        XCTAssertEqual(reinstalled.pendingRevocationIDs, [first.identity.id])
        XCTAssertNil(reinstalled.recoveredVaultKey)
        XCTAssertNil(try vaultStore.read())
    }

    func testVaultWriteFailureDoesNotMarkInstallationComplete() throws {
        let identityStore = MemorySecureValueStore()
        let vaultStore = FailingWriteSecureValueStore()
        let markerStore = MemoryInstallMarkerStore()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: vaultStore,
            markerStore: markerStore
        )

        XCTAssertThrowsError(try bootstrapper.bootstrap())
        XCTAssertFalse(markerStore.isInstalled())
        XCTAssertNil(try identityStore.read())
    }

    func testVaultKeyRequires256Bits() {
        XCTAssertThrowsError(try VaultKey(data: Data(repeating: 0, count: 31))) { error in
            XCTAssertEqual(error as? KeyRecoveryError, .invalidVaultKey)
        }
    }

    func testLegacyInstallationIdentityAddsKeyAgreementKeyWithoutChangingID() throws {
        let legacyID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let signingKey = Curve25519.Signing.PrivateKey().rawRepresentation
        let identityStore = MemorySecureValueStore()
        try identityStore.write(
            JSONEncoder().encode(
                LegacyInstallationIdentity(
                    id: legacyID,
                    signingPrivateKey: signingKey,
                    pendingRevocationIDs: []
                )
            )
        )
        let markerStore = MemoryInstallMarkerStore()
        markerStore.markInstalled()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: MemorySecureValueStore(),
            markerStore: markerStore
        )

        let upgraded = try bootstrapper.bootstrap().identity
        let relaunched = try bootstrapper.bootstrap().identity

        XCTAssertEqual(upgraded.id, legacyID)
        XCTAssertEqual(upgraded.signingPrivateKey, signingKey)
        XCTAssertEqual(try upgraded.keyAgreementPublicKey().count, 32)
        XCTAssertEqual(relaunched.keyAgreementPrivateKey, upgraded.keyAgreementPrivateKey)
    }
}

private struct LegacyInstallationIdentity: Encodable {
    let id: UUID
    let signingPrivateKey: Data
    let pendingRevocationIDs: [UUID]
}

private final class MemorySecureValueStore: SecureValueStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var value: Data?

    func read() -> Data? {
        lock.withLock { value }
    }

    func write(_ value: Data) {
        lock.withLock { self.value = value }
    }

    func delete() {
        lock.withLock { value = nil }
    }
}

private final class FailingWriteSecureValueStore: SecureValueStoring, @unchecked Sendable {
    private enum Failure: Error {
        case writeFailed
    }

    func read() -> Data? {
        nil
    }

    func write(_ value: Data) throws {
        throw Failure.writeFailed
    }

    func delete() {}
}

private final class MemoryInstallMarkerStore: InstallMarkerStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var installed = false

    func isInstalled() -> Bool {
        lock.withLock { installed }
    }

    func markInstalled() {
        lock.withLock { installed = true }
    }

    func removeMarkerToSimulateReinstall() {
        lock.withLock { installed = false }
    }
}
