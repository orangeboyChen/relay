import SwiftUI

struct ProfileScreen: View {
    let devices: [DeviceLineIdentity]
    let keyRecoveryState: KeyRecoveryState

    var body: some View {
        List {
            AccountSummarySection()
            DeviceSummarySection(devices: devices)
            SecuritySummarySection(keyRecoveryState: keyRecoveryState)

            Section {
                NavigationLink("Device Management") {
                    DeviceManagementScreen(devices: devices)
                }
                NavigationLink("Settings") {
                    SettingsScreen(devices: devices, keyRecoveryState: keyRecoveryState)
                }
            }
        }
        .navigationTitle("Me")
    }
}

private struct AccountSummarySection: View {
    var body: some View {
        Section("Account") {
            LabeledContent("Owner", value: "admin")
            LabeledContent("Server") {
                Label("Connected", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
            }
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
        ProfileScreen(devices: PreviewFixtures.devices, keyRecoveryState: .available)
    }
}

#endif
