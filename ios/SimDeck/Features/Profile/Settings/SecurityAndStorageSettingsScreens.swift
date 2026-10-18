import SwiftUI

struct SecuritySettingsScreen: View {
    let model: AppModel
    @State private var isRegisteringPasskey = false
    @State private var errorMessage: String?

    var body: some View {
        List {
            Section("Authentication") {
                Button("Register Passkey") {
                    isRegisteringPasskey = true
                    Task {
                        defer { isRegisteringPasskey = false }
                        do { try await model.registerPasskey() }
                        catch { errorMessage = error.localizedDescription }
                    }
                }
                .disabled(isRegisteringPasskey || !model.serverConnection.isSignedIn)
                NavigationLink("Trusted iPhones") {
                    TrustedDevicesScreen()
                }
            }
            Section("Encryption") {
                LabeledContent("End-to-end encryption", value: "Active")
                LabeledContent("Recovery") {
                    KeyRecoveryStatusLabel(state: model.keyRecoveryState)
                }
            }
            Section {
                Button("Revoke This Session", role: .destructive) { }
            }
        }
        .navigationTitle("Security")
        .alert("Passkey", isPresented: .init(
            get: { errorMessage != nil }, set: { if !$0 { errorMessage = nil } }
        )) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }
}

struct TrustedDevicesScreen: View {
    var body: some View {
        List {
            Label("This iPhone", systemImage: "iphone.gen3")
            Label("Home iPhone", systemImage: "iphone.gen3")
        }
        .navigationTitle("Trusted iPhones")
    }
}

struct StorageSettingsScreen: View {
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

struct AdvancedSettingsScreen: View {
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

struct AboutScreen: View {
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

struct DataDeletionScreen: View {
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
#Preview("Security Settings") {
    NavigationStack { SecuritySettingsScreen(model: .preview()) }
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
