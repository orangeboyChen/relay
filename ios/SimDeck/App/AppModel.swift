import Foundation
import Observation

@MainActor
@Observable
final class AppModel {
    var devices: [DeviceLineIdentity]
    var callHistory: CallHistoryState
    var messages: MessageListState
    var lastSuccessfulDialLineID: UUID?
    var selectedTab: AppTab
    var keyRecoveryState: KeyRecoveryState
    var installationID: UUID?
    var pendingInstallationRevocationIDs: [UUID]
    let callStarter: any CallStarting
    let installationBootstrapper: any InstallationBootstrapping

    init(
        devices: [DeviceLineIdentity] = [],
        callHistory: CallHistoryState = .loaded([]),
        messages: MessageListState = .loaded([]),
        lastSuccessfulDialLineID: UUID? = nil,
        selectedTab: AppTab = .calls,
        keyRecoveryState: KeyRecoveryState = .checking,
        callStarter: any CallStarting = CallKitCallStarter(),
        installationBootstrapper: any InstallationBootstrapping = InstallationBootstrapper.live()
    ) {
        self.devices = devices
        self.callHistory = callHistory
        self.messages = messages
        self.lastSuccessfulDialLineID = lastSuccessfulDialLineID
        self.selectedTab = selectedTab
        self.keyRecoveryState = keyRecoveryState
        self.pendingInstallationRevocationIDs = []
        self.callStarter = callStarter
        self.installationBootstrapper = installationBootstrapper
    }

    func bootstrapInstallation() {
        do {
            let result = try installationBootstrapper.bootstrap()
            installationID = result.identity.id
            pendingInstallationRevocationIDs = result.pendingRevocationIDs
            keyRecoveryState = result.recoveredVaultKey == nil ? .approvalRequired : .available
        } catch {
            keyRecoveryState = .unavailable
        }
    }
}

enum KeyRecoveryState: Equatable, Sendable {
    case checking
    case available
    case approvalRequired
    case unavailable
}

enum AppTab: Hashable {
    case calls
    case messages
    case profile
}
