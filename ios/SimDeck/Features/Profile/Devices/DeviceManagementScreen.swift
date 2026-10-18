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
                            ForEach(group.lines) { line in
                                NavigationLink {
                                    DeviceSettingsScreen(model: model, line: line)
                                } label: {
                                    DeviceSlotRow(line: line)
                                }
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
        .task {
            await model.refreshDevices()
        }
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
        HStack(spacing: 8) {
            Image(systemName: "externaldrive.connected.to.line.below")
            Text(group.deviceName)
                .font(.headline)
            Spacer()
            Text(group.lines.contains(where: \.isOnline) ? String(localized: "Online") : String(localized: "Offline"))
                .font(.caption)
                .foregroundStyle(group.lines.contains(where: \.isOnline) ? .green : .secondary)
        }
        .textCase(nil)
    }
}

private struct DeviceSlotRow: View {
    let line: DeviceLineIdentity

    var body: some View {
        HStack(spacing: 12) {
            SIMBadge(line: line)
            VStack(alignment: .leading, spacing: 3) {
                Text(line.slotIndex.map { String(localized: "Slot \($0)") } ?? String(localized: "Slot"))
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text(line.displayName)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 12)
            Text(line.isOnline ? String(localized: "Online") : String(localized: "Offline"))
                .font(.caption)
                .foregroundStyle(line.isOnline ? .green : .secondary)
        }
        .frame(minHeight: 44)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Slot \(line.slotIndex ?? 1), \(line.displayName), \(line.radioAccessibilityDescription)")
    }
}

private struct DeviceManagementEmptyState: View {
    let addDevice: () -> Void

    var body: some View {
        ContentUnavailableView {
            Label("No Devices", systemImage: "simcard")
        } description: {
            Text("Turn on Device Discovery, then start one Linux modem container for each SIM.")
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
        DeviceManagementScreen(model: .preview(selectedTab: .profile))
    }
}

#Preview("Device Management - Empty") {
    NavigationStack {
        DeviceManagementScreen(model: .preview(selectedTab: .profile, devices: []))
    }
    .preferredColorScheme(.dark)
}
#endif
