import SwiftUI
import UIKit

enum DevicePairingTokenState: Equatable {
    case idle
    case loading
    case loaded(DevicePairingToken)
    case failed(String)
}

enum DevicePairingDetector {
    static func newDevice(
        previousIDs: Set<UUID>,
        devices: [DeviceLineIdentity]
    ) -> DeviceLineIdentity? {
        devices.first { !previousIDs.contains($0.id) }
    }
}

struct AddDeviceScreen: View {
    let model: AppModel

    @Environment(\.dismiss) private var dismiss
    @State private var tokenState: DevicePairingTokenState
    @State private var isChecking = false
    @State private var checkMessage: String?
    @State private var pairedDevice: DeviceLineIdentity?
    private let initialDeviceIDs: Set<UUID>

    init(
        model: AppModel,
        initialTokenState: DevicePairingTokenState = .idle
    ) {
        self.model = model
        initialDeviceIDs = Set(model.devices.map(\.id))
        _tokenState = State(initialValue: initialTokenState)
    }

    var body: some View {
        NavigationStack {
            Form {
                PairingCodeSection(
                    state: tokenState,
                    generate: generatePairingToken,
                    copy: copyPairingToken
                )
                PairingInstructionsSection()
                PairingCheckSection(
                    isChecking: isChecking,
                    message: checkMessage,
                    check: checkForDevice
                )
            }
            .navigationTitle("Add Device")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
            .task {
                if tokenState == .idle {
                    await loadPairingToken()
                }
            }
            .alert(
                "Device Paired",
                isPresented: Binding(
                    get: { pairedDevice != nil },
                    set: { if !$0 { pairedDevice = nil } }
                ),
                presenting: pairedDevice
            ) { _ in
                Button("Done") {
                    dismiss()
                }
            } message: { device in
                Text("SIM \(device.numericID) is ready to configure.")
            }
        }
    }

    private func generatePairingToken() {
        Task { await loadPairingToken() }
    }

    private func loadPairingToken() async {
        tokenState = .loading
        checkMessage = nil
        do {
            tokenState = .loaded(try await model.createDevicePairingToken())
        } catch {
            tokenState = .failed(error.localizedDescription)
        }
    }

    private func copyPairingToken(_ value: String) {
        UIPasteboard.general.string = value
        checkMessage = String(localized: "Pairing code copied.")
    }

    private func checkForDevice() {
        Task {
            isChecking = true
            checkMessage = nil
            await model.refreshDevices()
            isChecking = false
            if let device = DevicePairingDetector.newDevice(
                previousIDs: initialDeviceIDs,
                devices: model.devices
            ) {
                pairedDevice = device
            } else {
                checkMessage = String(localized: "No new device found yet.")
            }
        }
    }
}

private struct PairingCodeSection: View {
    let state: DevicePairingTokenState
    let generate: () -> Void
    let copy: (String) -> Void

    var body: some View {
        Section {
            switch state {
            case .idle, .loading:
                HStack {
                    ProgressView()
                    Text("Generating a secure code…")
                        .foregroundStyle(.secondary)
                }
                .frame(minHeight: 44)
            case .loaded(let token):
                Text(token.value)
                    .font(.system(.body, design: .monospaced, weight: .semibold))
                    .textSelection(.enabled)
                    .accessibilityLabel("Pairing code")
                LabeledContent("Expires") {
                    Text(token.expiresAt, format: .dateTime.hour().minute().second())
                        .foregroundStyle(.secondary)
                }
                Button {
                    copy(token.value)
                } label: {
                    Label("Copy Pairing Code", systemImage: "doc.on.doc")
                }
                Button("Generate New Code", action: generate)
            case .failed(let message):
                Label(message, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
                Button("Try Again", action: generate)
            }
        } header: {
            Text("One-Time Pairing Code")
        } footer: {
            Text("The code expires after ten minutes and can pair only one modem.")
        }
    }
}

private struct PairingInstructionsSection: View {
    var body: some View {
        Section {
            PairingInstructionRow(
                number: 1,
                title: "Save the code as a Docker secret",
                detail: "secrets/modem-1-pairing-token"
            )
            PairingInstructionRow(
                number: 2,
                title: "Mount the secret in the container",
                detail: "/run/secrets/sim_deck_pairing_token"
            )
            PairingInstructionRow(
                number: 3,
                title: "Start one container for this modem",
                detail: "Use unique device mappings and a unique state volume."
            )
        } header: {
            Text("Linux Device")
        } footer: {
            Text("For multiple SIMs, create a separate code, container, state volume, and hardware mapping for each modem.")
        }
    }
}

private struct PairingInstructionRow: View {
    let number: Int
    let title: LocalizedStringResource
    let detail: LocalizedStringResource

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text(number, format: .number)
                .font(.caption.bold())
                .foregroundStyle(.white)
                .frame(width: 24, height: 24)
                .background(.tint, in: Circle())
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                Text(detail)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        }
        .padding(.vertical, 4)
    }
}

private struct PairingCheckSection: View {
    let isChecking: Bool
    let message: String?
    let check: () -> Void

    var body: some View {
        Section {
            Button(action: check) {
                HStack {
                    Text("Check for Device")
                    if isChecking {
                        Spacer()
                        ProgressView()
                    }
                }
            }
            .disabled(isChecking)
            if let message {
                Text(message)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

#if DEBUG
#Preview("Pair Device") {
    AddDeviceScreen(
        model: .preview(selectedTab: .profile),
        initialTokenState: .loaded(
            DevicePairingToken(
                value: "sk_pair_8k3w-j19q-x7m2",
                expiresAt: PreviewFixtures.now.addingTimeInterval(600)
            )
        )
    )
}

#Preview("Pair Device - Error") {
    AddDeviceScreen(
        model: .preview(selectedTab: .profile),
        initialTokenState: .failed("The server request failed.")
    )
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility3)
}
#endif
