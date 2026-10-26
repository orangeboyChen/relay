// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct NodeManagementScreen: View {
    let model: AppModel

    @State private var isAddingNode = false

    var body: some View {
        Group {
            if model.devices.isEmpty {
                NodeManagementEmptyState {
                    isAddingNode = true
                }
            } else {
                List {
                    ForEach(NodeGroup.groups(from: model.devices)) { node in
                        Section {
                            ForEach(node.devices) { device in
                                NodeDeviceHeaderRow(group: device)
                                NavigationLink {
                                    DeviceSettingsScreen(model: model, line: device.lines[0])
                                } label: {
                                    Label("Device Settings", systemImage: "gearshape")
                                }
                                ForEach(device.lines) { line in
                                    DeviceSlotListRow(model: model, line: line)
                                }
                            }
                        } header: {
                            NodeHeader(node: node)
                        }
                    }
                }
                .refreshable {
                    await model.refreshDevices()
                }
            }
        }
        .navigationTitle("Node Management")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    isAddingNode = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Node")
            }
        }
        .sheet(isPresented: $isAddingNode) {
            AddDeviceScreen(model: model)
        }
        .task {
            await model.refreshDevices()
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }
}

/// A node is the host/container that owns one or more modem devices.
/// The current owner API exposes device records only, so each record is kept
/// as a one-device node until the node identity fields are added to the API.
private struct NodeGroup: Identifiable {
    let id: UUID
    let name: String
    let devices: [DeviceGroup]

    static func groups(from lines: [DeviceLineIdentity]) -> [NodeGroup] {
        DeviceGroup.groups(from: lines).map { group in
            NodeGroup(id: group.id, name: group.deviceName, devices: [group])
        }
    }
}

private struct NodeHeader: View {
    let node: NodeGroup

    var body: some View {
        HStack(spacing: 8) {
            Label {
                Text(node.name)
                    .font(.headline)
            } icon: {
                Image(systemName: "server.rack")
            }
            Spacer(minLength: 8)
            Text(node.devices.contains { $0.lines.contains(where: \.isOnline) }
                 ? String(localized: "Online")
                 : String(localized: "Offline"))
                .font(.caption)
                .foregroundStyle(node.devices.contains { $0.lines.contains(where: \.isOnline) } ? .green : .secondary)
        }
        .textCase(nil)
    }
}

private struct NodeDeviceHeaderRow: View {
    let group: DeviceGroup

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "externaldrive.connected.to.line.below")
                .foregroundStyle(.secondary)
            Text(group.deviceName)
                .font(.subheadline.weight(.semibold))
                .lineLimit(1)
            Spacer(minLength: 0)
        }
        .frame(minHeight: 32)
        .accessibilityAddTraits(.isHeader)
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

private struct NodeManagementEmptyState: View {
    let addNode: () -> Void

    var body: some View {
        ContentUnavailableView {
            Label("No Nodes", systemImage: "server.rack")
        } description: {
            Text("Turn on Node Discovery, then start the modem container on the host.")
        } actions: {
            Button("Add Node") {
                addNode()
            }
                .buttonStyle(.borderedProminent)
        }
    }
}

#if DEBUG
#Preview("Node Management") {
    NavigationStack {
        NodeManagementScreen(
            model: .preview(
                selectedTab: .profile,
                devices: PreviewFixtures.deviceManagementDevices
            )
        )
    }
}

#Preview("Node Management - Empty") {
    NavigationStack {
        NodeManagementScreen(model: .preview(selectedTab: .profile, devices: []))
    }
    .preferredColorScheme(.dark)
}

#Preview("Device Management - Compatibility") {
    NavigationStack {
        DeviceManagementScreen(model: .preview(selectedTab: .profile, devices: PreviewFixtures.deviceManagementDevices))
    }
}
#endif

/// Compatibility wrapper for previews and older internal navigation callers.
struct DeviceManagementScreen: View {
    let model: AppModel

    var body: some View {
        NodeManagementScreen(model: model)
    }
}
