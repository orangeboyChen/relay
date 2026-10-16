import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class OnboardingTests: XCTestCase {
    func testFreshInstallPresentsOnboarding() {
        let store = OnboardingStoreFake(isCompleted: false)

        let model = AppModel(onboardingStore: store)

        XCTAssertTrue(model.isOnboardingPresented)
    }

    func testCompletingOnboardingPersistsAndDismissesFlow() {
        let store = OnboardingStoreFake(isCompleted: false)
        let model = AppModel(onboardingStore: store)

        model.completeOnboarding()

        XCTAssertFalse(model.isOnboardingPresented)
        XCTAssertTrue(store.isCompleted)
    }

    func testCompletedOnboardingDoesNotAppearOnRelaunch() {
        let store = OnboardingStoreFake(isCompleted: true)

        let model = AppModel(onboardingStore: store)

        XCTAssertFalse(model.isOnboardingPresented)
    }

    func testReinstallMarkerLossPresentsOnboardingEvenWhenRecoveryKeyExists() throws {
        let identityStore = OnboardingSecureValueStore()
        let vaultStore = OnboardingSecureValueStore()
        let installMarker = OnboardingInstallMarkerStore()
        let bootstrapper = InstallationBootstrapper(
            identityStore: identityStore,
            vaultStore: vaultStore,
            markerStore: installMarker
        )
        _ = try bootstrapper.bootstrap()
        try bootstrapper.saveVaultKey(.random())
        installMarker.remove()

        let model = AppModel(
            installationBootstrapper: bootstrapper,
            onboardingStore: OnboardingStoreFake(isCompleted: false)
        )
        model.bootstrapInstallation()

        XCTAssertTrue(model.isOnboardingPresented)
        XCTAssertEqual(model.keyRecoveryState, .available)
        XCTAssertFalse(model.pendingInstallationRevocationIDs.isEmpty)
    }
}
@MainActor
private final class OnboardingStoreFake: OnboardingStoring {
    private(set) var isCompleted: Bool

    init(isCompleted: Bool) {
        self.isCompleted = isCompleted
    }

    func markCompleted() {
        isCompleted = true
    }
}

private final class OnboardingSecureValueStore: SecureValueStoring, @unchecked Sendable {
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

private final class OnboardingInstallMarkerStore: InstallMarkerStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var installed = false

    func isInstalled() -> Bool {
        lock.withLock { installed }
    }

    func markInstalled() {
        lock.withLock { installed = true }
    }

    func remove() {
        lock.withLock { installed = false }
    }
}
