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
                List(model.devices) { line in
                    NavigationLink {
                        DeviceSettingsScreen(model: model, line: line)
                    } label: {
                        DeviceManagementRow(line: line)
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
    }
}

private struct DeviceManagementRow: View {
    let line: DeviceLineIdentity

    var body: some View {
        HStack(spacing: 12) {
            SIMBadge(line: line)
            VStack(alignment: .leading, spacing: 3) {
                Text(line.deviceName.isEmpty ? String(localized: "Unnamed Device") : line.deviceName)
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
