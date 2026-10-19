import SwiftUI

struct SecuritySettingsScreen: View {
    let model: AppModel
    @State private var isRegisteringPasskey = false
    @State private var isRevokingSession = false
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
                    TrustedDevicesScreen(model: model)
                }
            }
            Section("Encryption") {
                LabeledContent("End-to-end encryption", value: "Active")
                LabeledContent("Recovery") {
                    KeyRecoveryStatusLabel(state: model.keyRecoveryState)
                }
            }
            Section {
                Button("Revoke This Session", role: .destructive) {
                    isRevokingSession = true
                    Task {
                        defer { isRevokingSession = false }
                        do { try await model.revokeCurrentSession() }
                        catch { errorMessage = error.localizedDescription }
                    }
                }
                .disabled(isRevokingSession || !model.serverConnection.isSignedIn)
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
    let model: AppModel

    var body: some View {
        List {
            if let installationID = model.installationID {
                LabeledContent("This iPhone", value: String(installationID.uuidString.prefix(8)) + "…")
            } else {
                ContentUnavailableView("Not Registered", systemImage: "iphone.slash", description: Text("Sign in to manage trusted iPhones."))
            }
            Text("Other trusted iPhones are managed during installation approval.")
                .font(.footnote)
                .foregroundStyle(.secondary)
        }
        .navigationTitle("Trusted iPhones")
    }
}

struct StorageSettingsScreen: View {
    let model: AppModel
    @State private var localSearchIndex = true
    @State private var autoDownloadMMS = true
    @State private var errorMessage: String?

    var body: some View {
        Form {
            Toggle("Local search index", isOn: $localSearchIndex)
            Toggle("Automatically download MMS", isOn: $autoDownloadMMS)
            LabeledContent("Cache size", value: "24 MB")
            Button("Clear Cache", role: .destructive) {
                do { try model.clearLocalMessageCache() }
                catch { errorMessage = error.localizedDescription }
            }
        }
        .navigationTitle("Messages & Storage")
        .alert("Clear Cache", isPresented: .init(get: { errorMessage != nil }, set: { if !$0 { errorMessage = nil } })) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: { Text(errorMessage ?? "") }
    }
}

struct AdvancedSettingsScreen: View {
    let connection: ServerConnectionManager
    private var diagnosticReport: String {
        "Sim Deck diagnostics\nServer: \(connection.address.isEmpty ? "Not configured" : "Configured")\nState: \(String(describing: connection.state))"
    }

    var body: some View {
        Form {
            Section("Media Ports") {
                LabeledContent("UDP", value: "Automatic")
                LabeledContent("TCP", value: "Automatic")
                Text("Media ports are supplied by the server and cannot be changed on this iPhone.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Section("Diagnostics") {
                ShareLink(item: diagnosticReport) {
                    Label("Export Redacted Diagnostics", systemImage: "square.and.arrow.up")
                }
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
    var body: some View {
        Form {
            Section {
                Text("This permanently deletes the owner account, devices, messages, attachments, and call history from the server.")
            }
            Section {
                Label("Account deletion is not available until the server exposes the authenticated deletion endpoint.", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.orange)
            }
        }
        .navigationTitle("Delete Data")
    }
}

#if DEBUG
#Preview("Security Settings") {
    NavigationStack { SecuritySettingsScreen(model: .preview()) }
}

#Preview("Trusted iPhones") {
    NavigationStack { TrustedDevicesScreen(model: .preview()) }
}

#Preview("Storage Settings") {
    NavigationStack { StorageSettingsScreen(model: .preview()) }
}

#Preview("Advanced Settings") {
    NavigationStack { AdvancedSettingsScreen(connection: AppModel.preview().serverConnection) }
}

#Preview("About") {
    NavigationStack { AboutScreen() }
}

#Preview("Delete Data") {
    NavigationStack { DataDeletionScreen() }
}
#endif
