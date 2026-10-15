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
        let vaultKey = try VaultKey(data: Data(repeating: 7, count: VaultKey.length))
        try bootstrapper.saveVaultKey(vaultKey)

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

    func testVaultKeyRequires256Bits() {
        XCTAssertThrowsError(try VaultKey(data: Data(repeating: 0, count: 31))) { error in
            XCTAssertEqual(error as? KeyRecoveryError, .invalidVaultKey)
        }
    }
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
