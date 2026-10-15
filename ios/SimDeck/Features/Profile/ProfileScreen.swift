import SwiftUI

struct ProfileScreen: View {
    let model: AppModel

    var body: some View {
        List {
            AccountSummarySection(connection: model.serverConnection)
            DeviceSummarySection(devices: model.devices)
            SecuritySummarySection(keyRecoveryState: model.keyRecoveryState)

            Section {
                NavigationLink("Device Management") {
                    DeviceManagementScreen(devices: model.devices)
                }
                NavigationLink("Settings") {
                    SettingsScreen(model: model)
                }
            }
        }
        .navigationTitle("Me")
    }
}

private struct AccountSummarySection: View {
    let connection: ServerConnectionManager

    var body: some View {
        Section("Account") {
            LabeledContent("Owner", value: "admin")
            ServerConnectionStatusRow("Server", state: connection.state)
        }
    }
}

private struct DeviceSummarySection: View {
    let devices: [DeviceLineIdentity]

    var body: some View {
        Section("Devices") {
            LabeledContent("Bound devices", value: devices.count.formatted())
            LabeledContent("Online", value: devices.filter(\.isOnline).count.formatted())
        }
    }
}

private struct SecuritySummarySection: View {
    let keyRecoveryState: KeyRecoveryState

    var body: some View {
        Section("Security") {
            LabeledContent("End-to-end encryption") {
                Label("Active", systemImage: "lock.shield.fill")
                    .foregroundStyle(.green)
            }
            LabeledContent("iCloud Keychain recovery") {
                KeyRecoveryStatusLabel(state: keyRecoveryState)
            }
        }
    }
}

struct KeyRecoveryStatusLabel: View {
    let state: KeyRecoveryState

    var body: some View {
        switch state {
        case .checking:
            Label("Checking", systemImage: "icloud")
                .foregroundStyle(.secondary)
        case .available:
            Label("Available", systemImage: "icloud.fill")
                .foregroundStyle(.blue)
        case .approvalRequired:
            Label("Approval required", systemImage: "person.badge.key")
                .foregroundStyle(.orange)
        case .unavailable:
            Label("Unavailable", systemImage: "exclamationmark.icloud")
                .foregroundStyle(.red)
        }
    }
}

#if DEBUG
#Preview("Me") {
    NavigationStack {
        ProfileScreen(model: .preview(selectedTab: .profile))
    }
}

#endif
