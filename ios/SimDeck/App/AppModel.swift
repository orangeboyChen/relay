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
    let serverConnection: ServerConnectionManager
    let callCoordinator: CallKitCoordinator
    let installationBootstrapper: any InstallationBootstrapping

    init(
        devices: [DeviceLineIdentity] = [],
        callHistory: CallHistoryState = .loaded([]),
        messages: MessageListState = .loaded([]),
        lastSuccessfulDialLineID: UUID? = nil,
        selectedTab: AppTab = .calls,
        keyRecoveryState: KeyRecoveryState = .checking,
        serverConnection: ServerConnectionManager? = nil,
        callCoordinator: CallKitCoordinator? = nil,
        installationBootstrapper: any InstallationBootstrapping = InstallationBootstrapper.live()
    ) {
        let resolvedServerConnection = serverConnection ?? .live()
        self.devices = devices
        self.callHistory = callHistory
        self.messages = messages
        self.lastSuccessfulDialLineID = lastSuccessfulDialLineID
        self.selectedTab = selectedTab
        self.keyRecoveryState = keyRecoveryState
        self.pendingInstallationRevocationIDs = []
        self.serverConnection = resolvedServerConnection
        self.callCoordinator = callCoordinator ?? CallKitCoordinator(
            transport: LiveCallTransport(api: resolvedServerConnection)
        )
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

    func refreshDevices() async {
        guard serverConnection.isSignedIn else {
            devices = []
            return
        }
        do {
            devices = try await serverConnection.loadDevices()
        } catch {
            devices = devices.map { device in
                var offlineDevice = device
                offlineDevice.isOnline = false
                return offlineDevice
            }
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
