// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct ProfileScreen: View {
    let model: AppModel

    var body: some View {
        List {
            AccountSummarySection(connection: model.serverConnection)
            SIMCardsSection(lines: model.devices)
            SecuritySummarySection(keyRecoveryState: model.keyRecoveryState)

            Section {
                NavigationLink {
                    DeviceManagementScreen(model: model)
                } label: {
                    Label("Device Management", systemImage: "simcard.2")
                }
                NavigationLink {
                    SIMManagementScreen(model: model)
                } label: {
                    Label("SIM Cards", systemImage: "simcard")
                }
                NavigationLink {
                    SettingsScreen(model: model)
                } label: {
                    Label("Settings", systemImage: "gearshape")
                }
            }
        }
        .navigationTitle("Me")
        .refreshable {
            await model.refreshDevices()
        }
    }
}

private struct AccountSummarySection: View {
    let connection: ServerConnectionManager

    var body: some View {
        Section("Account") {
            AdaptiveProfileStatusRow("Owner") {
                Text("admin")
                    .foregroundStyle(.secondary)
            }
            ServerConnectionStatusRow("Server", state: connection.state)
        }
    }
}

private struct SIMCardsSection: View {
    let lines: [DeviceLineIdentity]

    var body: some View {
        Section("SIM Cards") {
            if lines.isEmpty {
                EmptySIMCardsRow()
            } else {
                ForEach(lines) { line in
                    SIMRadioStatusRow(line: line)
                }
            }
        }
    }
}

private struct EmptySIMCardsRow: View {
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "simcard")
                .foregroundStyle(.secondary)
                .frame(width: 22)

            VStack(alignment: .leading, spacing: 3) {
                Text("No SIM Cards")
                Text("Add a device in Device Management.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
    }
}

struct SIMRadioStatusRow: View {
    let line: DeviceLineIdentity

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 12) {
                SIMLineDescription(line: line)

                if !dynamicTypeSize.isAccessibilitySize {
                    Spacer(minLength: 12)
                    SIMRadioStatusSummary(
                        isOnline: line.isOnline,
                        status: line.radioStatus
                    )
                }
            }

            if dynamicTypeSize.isAccessibilitySize {
                SIMRadioStatusSummary(
                    isOnline: line.isOnline,
                    status: line.radioStatus
                )
                .padding(.leading, 32)
            }
        }
        .frame(minHeight: 44)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel)
    }

    private var accessibilityLabel: Text {
        Text("SIM \(line.numericID), \(line.displayName), \(line.radioAccessibilityDescription)")
    }
}

private struct SIMLineDescription: View {
    let line: DeviceLineIdentity

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            SIMLabel(line: line, size: 20)
            if !line.deviceName.isEmpty {
                Text(line.deviceName)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }
}

private struct SIMRadioStatusSummary: View {
    let isOnline: Bool
    let status: CellularRadioStatus?

    var body: some View {
        VStack(alignment: .trailing, spacing: 2) {
            if !isOnline {
                Text("Offline")
                    .foregroundStyle(.secondary)
            } else if let status {
                Text(status.technology.displayName)
                    .font(.subheadline.weight(.semibold))
                if let signalBars = status.signalBars {
                    CellularSignalIndicator(bars: signalBars)
                } else {
                    Text("Signal unavailable")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            } else {
                Text("Online")
                Text("Signal unavailable")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .multilineTextAlignment(.trailing)
        .fixedSize(horizontal: true, vertical: false)
    }
}

private struct SecuritySummarySection: View {
    let keyRecoveryState: KeyRecoveryState

    var body: some View {
        Section("Security") {
            AdaptiveProfileStatusRow("End-to-end encryption") {
                ProfileStatusValue(
                    title: "Active",
                    systemImage: "lock.shield.fill",
                    color: .green
                )
            }
            AdaptiveProfileStatusRow("iCloud Keychain recovery") {
                KeyRecoveryStatusLabel(state: keyRecoveryState)
            }
        }
    }
}

private struct AdaptiveProfileStatusRow<Content: View>: View {
    let title: LocalizedStringKey
    @ViewBuilder let content: Content

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    init(_ title: LocalizedStringKey, @ViewBuilder content: () -> Content) {
        self.title = title
        self.content = content()
    }

    var body: some View {
        if dynamicTypeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: 8) {
                Text(title)
                content
            }
            .padding(.vertical, 4)
        } else {
            HStack(spacing: 12) {
                Text(title)
                Spacer(minLength: 12)
                content
                    .fixedSize(horizontal: true, vertical: false)
            }
        }
    }
}

struct KeyRecoveryStatusLabel: View {
    let state: KeyRecoveryState

    var body: some View {
        switch state {
        case .checking:
            ProfileStatusValue(
                title: "Checking",
                systemImage: "icloud",
                color: .secondary
            )
        case .available:
            ProfileStatusValue(
                title: "Available",
                systemImage: "icloud.fill",
                color: .blue
            )
        case .approvalRequired:
            ProfileStatusValue(
                title: "Approval required",
                systemImage: "person.badge.key",
                color: .orange
            )
        case .unavailable:
            ProfileStatusValue(
                title: "Unavailable",
                systemImage: "exclamationmark.icloud",
                color: .red
            )
        }
    }
}

private struct ProfileStatusValue: View {
    let title: LocalizedStringKey
    let systemImage: String
    let color: Color

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: systemImage)
            Text(title)
        }
        .foregroundStyle(color)
    }
}

#if DEBUG
#Preview("Me - Multiple SIMs") {
    NavigationStack {
        ProfileScreen(model: .preview(selectedTab: .profile))
    }
}

#Preview("Me - Empty") {
    NavigationStack {
        ProfileScreen(
            model: .preview(
                selectedTab: .profile,
                devices: [],
                keyRecoveryState: .checking
            )
        )
    }
}

#Preview("Me - Offline and Dark") {
    NavigationStack {
        ProfileScreen(
            model: .preview(
                selectedTab: .profile,
                devices: PreviewFixtures.devices.map { line in
                    var offlineLine = line
                    offlineLine.isOnline = false
                    offlineLine.radioStatus = nil
                    return offlineLine
                }
            )
        )
    }
    .preferredColorScheme(.dark)
}

#Preview("Me - Accessibility Chinese") {
    NavigationStack {
        ProfileScreen(model: .preview(selectedTab: .profile))
    }
    .environment(\.dynamicTypeSize, .accessibility5)
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}

#endif
