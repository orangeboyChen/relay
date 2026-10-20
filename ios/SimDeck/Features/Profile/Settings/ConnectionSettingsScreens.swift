// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit
import Contacts

struct ServerSettingsScreen: View {
    let model: AppModel

    var body: some View {
        Form {
            ServerConnectionSection(model: model)
            ServerVersionSection()
        }
        .navigationTitle("Server")
    }
}

private struct ServerConnectionSection: View {
    let model: AppModel
    @Bindable private var connection: ServerConnectionManager
    @State private var password = ""
    @State private var isWorking = false
    @State private var actionResult: LocalizedStringResource?
    @State private var actionError: String?

    init(model: AppModel) {
        self.model = model
        _connection = Bindable(wrappedValue: model.serverConnection)
    }

    var body: some View {
        Section("Server") {
            TextField("Server address", text: $connection.address)
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .disabled(connection.isSignedIn)

            ServerConnectionStatusRow("Status", state: connection.state)

            if connection.isSignedIn {
                Button("Test Connection") {
                    Task { await testConnection() }
                }
                .disabled(isWorking)

                Button("Sign Out", role: .destructive) {
                    Task { await signOut() }
                }
                .disabled(isWorking)
            } else {
                SecureField("Admin bootstrap password", text: $password)

                Button {
                    Task { await signIn() }
                } label: {
                    if isWorking {
                        ProgressView()
                    } else {
                        Text("Sign In")
                    }
                }
                .disabled(!canSignIn)
            }

            if let actionResult {
                Text(actionResult)
                    .foregroundStyle(.green)
            }
            if let actionError {
                Text(actionError)
                    .foregroundStyle(.red)
            }
        }
    }

    private var canSignIn: Bool {
        !isWorking
            && !connection.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !password.isEmpty
    }

    private func signIn() async {
        isWorking = true
        actionResult = nil
        actionError = nil
        defer { isWorking = false }
        do {
            try await model.signInToServer(password: password)
            password = ""
            actionResult = "Connection succeeded"
        } catch {
            actionError = error.localizedDescription
        }
    }

    private func testConnection() async {
        isWorking = true
        actionResult = nil
        actionError = nil
        defer { isWorking = false }
        do {
            try await model.testServerConnection()
            actionResult = "Connection succeeded"
        } catch {
            actionError = error.localizedDescription
        }
    }

    private func signOut() async {
        isWorking = true
        actionResult = nil
        actionError = nil
        defer { isWorking = false }
        do {
            try await model.signOutFromServer()
        } catch {
            actionError = error.localizedDescription
        }
    }
}

private struct ServerVersionSection: View {
    var body: some View {
        Section("Version") {
            LabeledContent("API", value: "v1")
        }
    }
}

struct ServerConnectionStatusLabel: View {
    let state: ServerConnectionState

    var body: some View {
        switch state {
        case .disconnected:
            ServerConnectionStateContent(
                title: "Not Connected",
                systemImage: "network.slash",
                color: .secondary
            )
        case .connecting:
            ServerConnectionStateContent(
                title: "Connecting",
                systemImage: "arrow.triangle.2.circlepath",
                color: .secondary
            )
        case .connected:
            ServerConnectionStateContent(
                title: "Connected",
                systemImage: "checkmark.circle.fill",
                color: .green
            )
        case .failed:
            ServerConnectionStateContent(
                title: "Connection Failed",
                systemImage: "exclamationmark.triangle.fill",
                color: .red
            )
        }
    }
}

private struct ServerConnectionStateContent: View {
    let title: LocalizedStringKey
    let systemImage: String
    let color: Color

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: systemImage)
            Text(title)
        }
        .foregroundStyle(color)
        .fixedSize(horizontal: false, vertical: true)
    }
}

struct ServerConnectionStatusRow: View {
    let title: LocalizedStringKey
    let state: ServerConnectionState

    init(_ title: LocalizedStringKey, state: ServerConnectionState) {
        self.title = title
        self.state = state
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack {
                Text(title)
                Spacer()
                ServerConnectionStatusLabel(state: state)
                    .fixedSize(horizontal: true, vertical: false)
            }

            VStack(alignment: .leading, spacing: 8) {
                Text(title)
                ServerConnectionStatusLabel(state: state)
            }
        }
    }
}

struct DefaultLineSettingsScreen: View {
    @Bindable var model: AppModel

    var body: some View {
        Form {
            LinePickerRow(
                "Default call SIM",
                lines: model.devices,
                selection: $model.defaultCallLineID
            )
            LinePickerRow(
                "Default message SIM",
                lines: model.devices,
                selection: $model.defaultMessageLineID
            )
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
    @AppStorage(NotificationPreferences.messageNotificationsKey, store: NotificationPreferences.sharedDefaults) private var messageNotifications = true
    @AppStorage(NotificationPreferences.incomingCallNotificationsKey, store: NotificationPreferences.sharedDefaults) private var incomingCallNotifications = true
    @AppStorage(NotificationPreferences.showMessagePreviewKey, store: NotificationPreferences.sharedDefaults) private var showMessagePreview = false

    var body: some View {
        Form {
            Toggle("Message notifications", isOn: Binding(
                get: { messageNotifications },
                set: { value in
                    messageNotifications = value
                    NotificationCenter.default.post(
                        name: .simDeckMessageNotificationPreferenceChanged,
                        object: value
                    )
                }
            ))
            Toggle("Incoming call notifications", isOn: Binding(
                get: { incomingCallNotifications },
                set: { value in
                    incomingCallNotifications = value
                    NotificationCenter.default.post(
                        name: .simDeckIncomingCallPreferenceChanged,
                        object: value
                    )
                }
            ))
            Toggle("Show message preview", isOn: $showMessagePreview)
        }
        .navigationTitle("Notifications")
    }
}

struct ContactsSettingsScreen: View {
    @State private var authorizationStatus = CNContactStore.authorizationStatus(for: .contacts)
    @State private var isRequesting = false

    var body: some View {
        Form {
            LabeledContent("Permission", value: statusLabel)
            if authorizationStatus == .notDetermined {
                Button {
                    requestAccess()
                } label: {
                    if isRequesting { ProgressView() } else { Text("Allow Contacts Access") }
                }
                .disabled(isRequesting)
            }
            Link("Open System Settings", destination: URL(string: UIApplication.openSettingsURLString)!)
        }
        .navigationTitle("Contacts")
    }

    private var statusLabel: String {
        switch authorizationStatus {
        case .authorized: "Allowed"
        case .denied, .restricted: "Denied"
        case .limited: "Limited"
        case .notDetermined: "Not requested"
        @unknown default: "Unknown"
        }
    }

    private func requestAccess() {
        isRequesting = true
        Task {
            let granted = (try? await CNContactStore().requestAccess(for: .contacts)) ?? false
            authorizationStatus = granted ? .authorized : CNContactStore.authorizationStatus(for: .contacts)
            isRequesting = false
        }
    }
}

#if DEBUG
#Preview("Server Settings") {
    NavigationStack { ServerSettingsScreen(model: .preview(selectedTab: .profile)) }
}

#Preview("Default Lines") {
    NavigationStack { DefaultLineSettingsScreen(model: .preview(selectedTab: .profile)) }
}

#Preview("Notification Settings") {
    NavigationStack { NotificationSettingsScreen() }
}

#Preview("Contacts Settings") {
    NavigationStack { ContactsSettingsScreen() }
}
#endif
