// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct DeviceManagementScreen: View {
    let model: AppModel

    @State private var isAddingDevice = false

    var body: some View {
        Group {
            if model.devices.isEmpty {
                DeviceManagementEmptyState {
                    isAddingDevice = true
                }
            } else {
                List {
                    ForEach(DeviceGroup.groups(from: model.devices)) { group in
                        Section {
                            NavigationLink {
                                DeviceSettingsScreen(model: model, line: group.lines[0])
                            } label: {
                                Label("Device Settings", systemImage: "externaldrive.badge.gearshape")
                            }
                            ForEach(group.lines) { line in
                                DeviceSlotListRow(model: model, line: line)
                            }
                        } header: {
                            DeviceGroupHeader(group: group)
                        }
                    }
                }
                .refreshable {
                    await model.refreshDevices()
                }
            }
        }
        .navigationTitle("Device Management")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    isAddingDevice = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Device")
            }
        }
        .sheet(isPresented: $isAddingDevice) {
            AddDeviceScreen(model: model)
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }
}

private struct DeviceGroup: Identifiable {
    let id: UUID
    let lines: [DeviceLineIdentity]

    var deviceName: String {
        lines.first?.deviceName.isEmpty == false ? lines[0].deviceName : String(localized: "Unnamed Device")
    }

    static func groups(from lines: [DeviceLineIdentity]) -> [DeviceGroup] {
        Dictionary(grouping: lines, by: \.deviceID)
            .map { DeviceGroup(id: $0.key, lines: $0.value.sorted { ($0.slotIndex ?? 0) < ($1.slotIndex ?? 0) }) }
            .sorted { $0.deviceName.localizedStandardCompare($1.deviceName) == .orderedAscending }
    }
}

private struct DeviceGroupHeader: View {
    let group: DeviceGroup

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 8) {
                DeviceGroupHeaderLabel(group: group)
                Spacer(minLength: 8)
                DeviceGroupHeaderStatus(group: group)
            }
            VStack(alignment: .leading, spacing: 4) {
                DeviceGroupHeaderLabel(group: group)
                DeviceGroupHeaderStatus(group: group)
            }
        }
        .textCase(nil)
    }
}

private struct DeviceGroupHeaderLabel: View {
    let group: DeviceGroup

    var body: some View {
        Label {
            Text(group.deviceName)
                .font(.headline)
        } icon: {
            Image(systemName: "externaldrive.connected.to.line.below")
        }
    }
}

private struct DeviceGroupHeaderStatus: View {
    let group: DeviceGroup

    var body: some View {
        Text(group.lines.contains(where: \.isOnline) ? String(localized: "Online") : String(localized: "Offline"))
            .font(.caption)
            .foregroundStyle(group.lines.contains(where: \.isOnline) ? .green : .secondary)
    }
}

private struct DeviceSlotListRow: View {
    let model: AppModel
    let line: DeviceLineIdentity

    var body: some View {
        NavigationLink {
            if line.simID != nil {
                SIMSettingsScreen(model: model, line: line)
            } else {
                SlotSettingsScreen(model: model, line: line)
            }
        } label: {
            VStack {
                DeviceSlotRow(line: line)
            }
        }
    }
}

private struct DeviceSlotRow: View {
    let line: DeviceLineIdentity

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            if line.simID == nil {
                Image(systemName: "simcard")
                    .foregroundStyle(.secondary)
                    .frame(width: 22, height: 22)
            }
            VStack(alignment: .leading, spacing: 3) {
                Text(line.slotIndex.map { String(localized: "Slot \($0)") } ?? String(localized: "Slot"))
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                if line.simID == nil {
                    Text("No SIM inserted")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    SIMLabel(line: line)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .frame(minHeight: 44)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Slot \(line.slotIndex ?? 1), \(line.simID == nil ? String(localized: "No SIM inserted") : line.displayName), \(line.radioAccessibilityDescription)")
    }
}

private struct DeviceManagementEmptyState: View {
    let addDevice: () -> Void

    var body: some View {
        ContentUnavailableView {
            Label("No Devices", systemImage: "simcard.2")
        } description: {
            Text("Turn on Device Discovery, then start one Linux modem container for each modem.")
        } actions: {
            Button("Add Device") {
                addDevice()
            }
                .buttonStyle(.borderedProminent)
        }
    }
}

#if DEBUG
#Preview("Device Management") {
    NavigationStack {
        DeviceManagementScreen(
            model: .preview(
                selectedTab: .profile,
                devices: PreviewFixtures.deviceManagementDevices
            )
        )
    }
}

#Preview("Device Management - Populated") {
    NavigationStack {
        DeviceManagementScreen(model: .preview(selectedTab: .profile, devices: []))
    }
    .preferredColorScheme(.dark)
}

#Preview("Device Management - Empty") {
    NavigationStack {
        DeviceManagementScreen(model: .preview(selectedTab: .profile, devices: PreviewFixtures.deviceManagementDevices))
    }
}
#endif
