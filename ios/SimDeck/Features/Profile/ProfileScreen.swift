// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

enum ProfileNavigationRoute: Hashable {
    case deviceManagement
    case simManagement
    case settings
    case sim(UUID)
}

struct ProfileScreen: View {
    let model: AppModel

    var body: some View {
        List {
            SIMCardsSection(
                model: model,
                lines: model.devices.filter { $0.simID != nil },
                serviceStates: model.slotSettingsByLineID
            )

            Section {
                NavigationLink(value: ProfileNavigationRoute.deviceManagement) {
                    Label("Device Management", systemImage: "simcard.2")
                }
                NavigationLink(value: ProfileNavigationRoute.simManagement) {
                    Label("SIM Cards", systemImage: "simcard")
                }
                NavigationLink(value: ProfileNavigationRoute.settings) {
                    Label("Settings", systemImage: "gearshape")
                }
            }
        }
        .refreshable {
            await model.refreshDevices()
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

}

private struct SIMCardsSection: View {
    let model: AppModel
    let lines: [DeviceLineIdentity]
    let serviceStates: [UUID: SlotSettings]

    var body: some View {
        Section("SIM Cards") {
            if lines.isEmpty {
                EmptySIMCardsRow()
            } else {
                ForEach(lines) { line in
                    NavigationLink(value: ProfileNavigationRoute.sim(line.id)) {
                        SIMRadioStatusRow(
                            line: line,
                            serviceState: serviceStates[line.id]
                        )
                    }
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
    let serviceState: SlotSettings?

    var body: some View {
        HStack(spacing: 12) {
            SIMLineDescription(line: line, serviceState: serviceState)
            Spacer(minLength: 12)
            SIMRadioStatusSummary(
                isOnline: line.isOnline,
                status: line.radioStatus
            )
        }
        .frame(minHeight: 44)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel)
    }

    private var accessibilityLabel: Text {
        Text(
            "SIM \(line.numericID), \(line.displayName), \(servicePresentation.title), \(line.radioAccessibilityDescription)"
        )
    }

    private var servicePresentation: ProfileSIMServicePresentation {
        ProfileSIMServicePresentation(line: line, state: serviceState)
    }
}

private struct SIMLineDescription: View {
    let line: DeviceLineIdentity
    let serviceState: SlotSettings?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            SIMLabel(line: line, size: 22)
            ProfileSIMServiceStatus(
                presentation: ProfileSIMServicePresentation(
                    line: line,
                    state: serviceState
                )
            )
        }
    }
}

private struct SIMRadioStatusSummary: View {
    let isOnline: Bool
    let status: CellularRadioStatus?

    var body: some View {
        Group {
            if isOnline, let status, let signalBars = status.signalBars {
                HStack(spacing: 5) {
                    CellularSignalIndicator(bars: signalBars)
                    Text(status.technology.displayName)
                }
                .font(.subheadline.weight(.semibold))
            } else if isOnline, let status {
                Text(status.technology.displayName)
                    .font(.subheadline.weight(.semibold))
            } else if isOnline {
                Text("Signal unavailable")
                    .foregroundStyle(.secondary)
            } else {
                Text("Offline")
                    .foregroundStyle(.secondary)
            }
        }
        .font(.subheadline)
        .multilineTextAlignment(.trailing)
        .fixedSize(horizontal: true, vertical: false)
    }
}

private struct ProfileSIMServicePresentation {
    let title: String
    let color: Color

    init(line: DeviceLineIdentity, state: SlotSettings?) {
        guard line.isOnline else {
            title = String(localized: "Offline")
            color = .secondary
            return
        }

        switch state {
        case let settings?:
            if settings.airplaneModeEnabled {
                title = String(localized: "Airplane Mode")
                color = .yellow
            } else {
                let services = [
                    settings.incomingCallsEnabled ? String(localized: "Calls") : nil,
                    settings.incomingSMSEnabled ? String(localized: "SMS") : nil,
                    settings.dataEnabled ? String(localized: "Data") : nil,
                ].compactMap { $0 }
                title = services.isEmpty
                    ? String(localized: "No Services Enabled")
                    : services.joined(separator: " · ")
                color = .green
            }
        case nil:
            // Keep the row quiet until the first settings snapshot arrives. A
            // refresh should not replace a previously rendered value with a
            // transient loading state.
            title = "—"
            color = .secondary
        }
    }
}

private struct ProfileSIMServiceStatus: View {
    let presentation: ProfileSIMServicePresentation

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(presentation.color)
                .frame(width: 7, height: 7)
                .accessibilityHidden(true)
            Text(presentation.title)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(1)
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

#Preview("Me - Airplane Mode") {
    NavigationStack {
        ProfileScreen(
            model: .preview(
                selectedTab: .profile,
                slotSettings: SlotSettings(
                    dataEnabled: true,
                    airplaneModeEnabled: true,
                    incomingCallsEnabled: true,
                    incomingSMSEnabled: true
                )
            )
        )
    }
}

#Preview("Me - No Enabled Services") {
    NavigationStack {
        ProfileScreen(
            model: .preview(
                selectedTab: .profile,
                slotSettings: SlotSettings(
                    dataEnabled: false,
                    airplaneModeEnabled: false,
                    incomingCallsEnabled: false,
                    incomingSMSEnabled: false
                )
            )
        )
    }
}

#Preview("Me - Accessibility Chinese") {
    NavigationStack {
        ProfileScreen(model: .preview(selectedTab: .profile))
    }
    .environment(\.dynamicTypeSize, .accessibility5)
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}

#endif
