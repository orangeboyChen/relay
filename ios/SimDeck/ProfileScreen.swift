import SwiftUI
import UIKit

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

private struct KeyRecoveryStatusLabel: View {
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

private struct DeviceSettingsScreen: View {
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

private struct MMSSettingsScreen: View {
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

struct SettingsScreen: View {
    let devices: [DeviceLineIdentity]
    let keyRecoveryState: KeyRecoveryState

    var body: some View {
        List {
            Section {
                SettingsLink("Server", symbol: "server.rack") {
                    ServerSettingsScreen()
                }
                SettingsLink("Default Lines", symbol: "simcard.2") {
                    DefaultLineSettingsScreen(lines: devices)
                }
                SettingsLink("Notifications", symbol: "bell") {
                    NotificationSettingsScreen()
                }
                SettingsLink("Contacts", symbol: "person.crop.circle") {
                    ContactsSettingsScreen()
                }
                SettingsLink("Security", symbol: "lock.shield") {
                    SecuritySettingsScreen(keyRecoveryState: keyRecoveryState)
                }
                SettingsLink("Messages & Storage", symbol: "externaldrive") {
                    StorageSettingsScreen()
                }
                SettingsLink("Advanced", symbol: "gearshape.2") {
                    AdvancedSettingsScreen()
                }
                SettingsLink("About", symbol: "info.circle") {
                    AboutScreen()
                }
            }

            Section {
                NavigationLink("Delete Account & Server Data") {
                    DataDeletionScreen()
                }
                .foregroundStyle(.red)
            }
        }
        .navigationTitle("Settings")
    }
}

private struct SettingsLink<Destination: View>: View {
    let title: LocalizedStringKey
    let symbol: String
    @ViewBuilder let destination: () -> Destination

    init(
        _ title: LocalizedStringKey,
        symbol: String,
        @ViewBuilder destination: @escaping () -> Destination
    ) {
        self.title = title
        self.symbol = symbol
        self.destination = destination
    }

    var body: some View {
        NavigationLink {
            destination()
        } label: {
            Label(title, systemImage: symbol)
        }
    }
}

private struct ServerSettingsScreen: View {
    @State private var address = "https://sim-deck.example.com"
    @State private var testResult: LocalizedStringResource?

    var body: some View {
        Form {
            Section("Server") {
                TextField("Server address", text: $address)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Button("Test Connection") {
                    testResult = "Connection succeeded"
                }
                if let testResult {
                    Text(testResult)
                        .foregroundStyle(.green)
                }
            }
            Section("Version") {
                LabeledContent("API", value: "v1")
                LabeledContent("Server", value: "0.1.0")
            }
        }
        .navigationTitle("Server")
    }
}

private struct DefaultLineSettingsScreen: View {
    let lines: [DeviceLineIdentity]
    @State private var defaultCallLineID: UUID?
    @State private var defaultMessageLineID: UUID?

    var body: some View {
        Form {
            LinePickerRow("Default call SIM", lines: lines, selection: $defaultCallLineID)
            LinePickerRow("Default message SIM", lines: lines, selection: $defaultMessageLineID)
        }
        .navigationTitle("Default Lines")
    }
}

private struct LinePickerRow: View {
    let title: LocalizedStringKey
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    init(_ title: LocalizedStringKey, lines: [DeviceLineIdentity], selection: Binding<UUID?>) {
        self.title = title
        self.lines = lines
        _selection = selection
    }

    var body: some View {
        Picker(title, selection: $selection) {
            Text("Automatic").tag(UUID?.none)
            ForEach(lines) { line in
                Text("SIM \(line.numericID) · \(line.displayName)").tag(Optional(line.id))
            }
        }
    }
}

private struct NotificationSettingsScreen: View {
    @State private var messageNotifications = true
    @State private var incomingCallNotifications = true
    @State private var showMessagePreview = false

    var body: some View {
        Form {
            Toggle("Message notifications", isOn: $messageNotifications)
            Toggle("Incoming call notifications", isOn: $incomingCallNotifications)
            Toggle("Show message preview", isOn: $showMessagePreview)
        }
        .navigationTitle("Notifications")
    }
}

private struct ContactsSettingsScreen: View {
    var body: some View {
        Form {
            LabeledContent("Permission", value: "Not requested")
            Link("Open System Settings", destination: URL(string: UIApplication.openSettingsURLString)!)
        }
        .navigationTitle("Contacts")
    }
}

private struct SecuritySettingsScreen: View {
    let keyRecoveryState: KeyRecoveryState

    var body: some View {
        List {
            Section("Authentication") {
                LabeledContent("Passkey", value: "Registered")
                NavigationLink("Trusted iPhones") {
                    TrustedDevicesScreen()
                }
            }
            Section("Encryption") {
                LabeledContent("End-to-end encryption", value: "Active")
                LabeledContent("Recovery") {
                    KeyRecoveryStatusLabel(state: keyRecoveryState)
                }
            }
            Section {
                Button("Revoke This Session", role: .destructive) { }
            }
        }
        .navigationTitle("Security")
    }
}

private struct TrustedDevicesScreen: View {
    var body: some View {
        List {
            Label("This iPhone", systemImage: "iphone.gen3")
            Label("Home iPhone", systemImage: "iphone.gen3")
        }
        .navigationTitle("Trusted iPhones")
    }
}

private struct StorageSettingsScreen: View {
    @State private var localSearchIndex = true
    @State private var autoDownloadMMS = true

    var body: some View {
        Form {
            Toggle("Local search index", isOn: $localSearchIndex)
            Toggle("Automatically download MMS", isOn: $autoDownloadMMS)
            LabeledContent("Cache size", value: "24 MB")
            Button("Clear Cache") { }
        }
        .navigationTitle("Messages & Storage")
    }
}

private struct AdvancedSettingsScreen: View {
    @State private var udpPort = "3478"
    @State private var tcpPort = "3478"

    var body: some View {
        Form {
            Section("Media Ports") {
                TextField("UDP port", text: $udpPort)
                    .keyboardType(.numberPad)
                TextField("TCP port", text: $tcpPort)
                    .keyboardType(.numberPad)
            }
            Section("Diagnostics") {
                Button("Export Redacted Logs") { }
            }
        }
        .navigationTitle("Advanced")
    }
}

private struct AboutScreen: View {
    var body: some View {
        Form {
            LabeledContent("App Version", value: "0.1.0 (1)")
            NavigationLink("Open Source Licenses") {
                Text("Open source license notices appear here.")
                    .padding()
                    .navigationTitle("Licenses")
            }
        }
        .navigationTitle("About")
    }
}

private struct DataDeletionScreen: View {
    @State private var isConfirming = false

    var body: some View {
        Form {
            Section {
                Text("This permanently deletes the owner account, devices, messages, attachments, and call history from the server.")
            }
            Section {
                Button("Authenticate & Delete", role: .destructive) {
                    isConfirming = true
                }
            }
        }
        .navigationTitle("Delete Data")
        .confirmationDialog(
            "Delete all account and server data?",
            isPresented: $isConfirming,
            titleVisibility: .visible
        ) {
            Button("Delete Permanently", role: .destructive) { }
            Button("Cancel", role: .cancel) { }
        } message: {
            Text("This action cannot be undone.")
        }
    }
}

#if DEBUG
#Preview("Me") {
    NavigationStack {
        ProfileScreen(devices: PreviewFixtures.devices, keyRecoveryState: .available)
    }
}

#Preview("Settings") {
    NavigationStack {
        SettingsScreen(devices: PreviewFixtures.devices, keyRecoveryState: .available)
    }
}

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

#Preview("Settings - Accessibility Chinese") {
    NavigationStack {
        SettingsScreen(devices: PreviewFixtures.devices, keyRecoveryState: .approvalRequired)
    }
    .environment(\.dynamicTypeSize, .accessibility5)
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}

#Preview("Server Settings") {
    NavigationStack { ServerSettingsScreen() }
}

#Preview("Default Lines") {
    NavigationStack { DefaultLineSettingsScreen(lines: PreviewFixtures.devices) }
}

#Preview("Notification Settings") {
    NavigationStack { NotificationSettingsScreen() }
}

#Preview("Contacts Settings") {
    NavigationStack { ContactsSettingsScreen() }
}

#Preview("Security Settings") {
    NavigationStack { SecuritySettingsScreen(keyRecoveryState: .available) }
}

#Preview("Trusted iPhones") {
    NavigationStack { TrustedDevicesScreen() }
}

#Preview("Storage Settings") {
    NavigationStack { StorageSettingsScreen() }
}

#Preview("Advanced Settings") {
    NavigationStack { AdvancedSettingsScreen() }
}

#Preview("About") {
    NavigationStack { AboutScreen() }
}

#Preview("Delete Data") {
    NavigationStack { DataDeletionScreen() }
}
#endif
