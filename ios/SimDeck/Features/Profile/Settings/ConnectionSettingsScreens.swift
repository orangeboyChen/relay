import SwiftUI
import UIKit

struct ServerSettingsScreen: View {
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

struct DefaultLineSettingsScreen: View {
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

struct NotificationSettingsScreen: View {
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

struct ContactsSettingsScreen: View {
    var body: some View {
        Form {
            LabeledContent("Permission", value: "Not requested")
            Link("Open System Settings", destination: URL(string: UIApplication.openSettingsURLString)!)
        }
        .navigationTitle("Contacts")
    }
}

#if DEBUG
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
#endif
