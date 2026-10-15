import SwiftUI

struct DeviceManagementScreen: View {
    let devices: [DeviceLineIdentity]

    var body: some View {
        List(devices) { line in
            NavigationLink {
                DeviceSettingsScreen(line: line)
            } label: {
                HStack(spacing: 12) {
                    SIMBadge(line: line)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(line.deviceName)
                            .lineLimit(1)
                        Text(line.displayName)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Text(line.isOnline ? "Online" : "Offline")
                        .font(.caption)
                        .foregroundStyle(line.isOnline ? .green : .secondary)
                }
            }
        }
        .navigationTitle("Device Management")
    }
}

struct DeviceSettingsScreen: View {
    @State private var numericID: Int
    @State private var deviceName: String
    @State private var lineName: String
    @State private var phoneNumber: String

    init(line: DeviceLineIdentity) {
        _numericID = State(initialValue: line.numericID)
        _deviceName = State(initialValue: line.deviceName)
        _lineName = State(initialValue: line.lineName)
        _phoneNumber = State(initialValue: line.phoneNumber)
    }

    var body: some View {
        Form {
            Section("Identity") {
                Stepper("SIM ID: \(numericID)", value: $numericID, in: 1...99)
                TextField("Device name", text: $deviceName)
                TextField("Line name", text: $lineName)
                TextField("Phone number", text: $phoneNumber)
                    .keyboardType(.phonePad)
            }

            Section("MMS") {
                NavigationLink("MMS Configuration") {
                    MMSSettingsScreen()
                }
            }

            Section {
                Button("Revoke Device", role: .destructive) { }
            }
        }
        .navigationTitle("Device")
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { }
            }
        }
    }
}

struct MMSSettingsScreen: View {
    @State private var apn = ""
    @State private var mmsc = ""
    @State private var proxy = ""
    @State private var sizeLimit = 1_024

    var body: some View {
        Form {
            TextField("APN", text: $apn)
            TextField("MMSC", text: $mmsc)
                .textInputAutocapitalization(.never)
            TextField("Proxy", text: $proxy)
                .textInputAutocapitalization(.never)
            Stepper("Size limit: \(sizeLimit) KB", value: $sizeLimit, in: 128...10_240, step: 128)
        }
        .navigationTitle("MMS Configuration")
    }
}

#if DEBUG
#Preview("Device Management - Dark") {
    NavigationStack {
        DeviceManagementScreen(devices: PreviewFixtures.devices)
    }
    .preferredColorScheme(.dark)
}

#Preview("Device Settings") {
    NavigationStack {
        DeviceSettingsScreen(line: PreviewFixtures.devices[0])
    }
}

#Preview("MMS Settings") {
    NavigationStack {
        MMSSettingsScreen()
    }
}
#endif
