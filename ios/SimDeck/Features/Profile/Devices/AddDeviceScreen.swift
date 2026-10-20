// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct AddDeviceScreen: View {
    let model: AppModel

    @Environment(\.dismiss) private var dismiss
    @State private var discovery: DeviceDiscoveryViewModel
    @State private var candidateToConfirm: DiscoveredDevice?

    init(model: AppModel, discovery: DeviceDiscoveryViewModel? = nil) {
        self.model = model
        _discovery = State(initialValue: discovery ?? DeviceDiscoveryViewModel(api: model.serverConnection))
    }

    var body: some View {
        @Bindable var discovery = discovery

        NavigationStack {
            Form {
                DeviceDiscoveryControlSection(
                    isEnabled: $discovery.isDiscoveryEnabled,
                    isChanging: discovery.isChangingDiscovery,
                    session: discovery.session,
                    now: discovery.now
                )
                DeviceDiscoveryResultsSection(
                    isEnabled: discovery.isDiscoveryEnabled,
                    loadState: discovery.loadState,
                    discoveries: discovery.discoveries,
                    decisionDeviceID: discovery.decisionDeviceID,
                    retry: { Task { await discovery.load() } },
                    select: { candidateToConfirm = $0 }
                )
                DeviceDiscoveryLinuxSection()
            }
            .navigationTitle("Add Device")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done", action: close)
                }
            }
            .task {
                guard discovery.automaticallyRefreshes else { return }
                await discovery.load()
            }
            .task {
                guard discovery.automaticallyRefreshes else { return }
                await discovery.poll()
            }
            .confirmationDialog(
                "Allow This Device?",
                isPresented: Binding(
                    get: { candidateToConfirm != nil },
                    set: { if !$0 { candidateToConfirm = nil } }
                ),
                titleVisibility: .visible,
                presenting: candidateToConfirm
            ) { candidate in
                Button("Allow") {
                    candidateToConfirm = nil
                    Task {
                        await discovery.approve(candidate)
                        await model.refreshDevices()
                    }
                }
                Button("Don’t Allow", role: .destructive) {
                    candidateToConfirm = nil
                    Task { await discovery.reject(candidate) }
                }
                Button("Cancel", role: .cancel) {}
            } message: { _ in
                Text("Only allow the modem if its UUID and fingerprint match the Linux device.")
            }
            .alert(
                "Device Added",
                isPresented: Binding(
                    get: { discovery.approvedDevice != nil },
                    set: { if !$0 { discovery.clearApproval() } }
                ),
                presenting: discovery.approvedDevice
            ) { _ in
                Button("Done", role: .cancel) { discovery.clearApproval() }
            } message: { approved in
                Text("The modem is now SIM \(approved.numericID). You can turn off Device Discovery.")
            }
        }
    }

    private func close() {
        dismiss()
    }
}

private struct DeviceDiscoveryControlSection: View {
    @Binding var isEnabled: Bool
    let isChanging: Bool
    let session: DeviceDiscoverySession?
    let now: Date

    var body: some View {
        Section {
            Toggle("Device Discovery", isOn: $isEnabled)
                .disabled(isChanging)
            if isChanging {
                HStack {
                    ProgressView()
                    if isEnabled {
                        Text("Starting discovery…")
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Stopping discovery…")
                            .foregroundStyle(.secondary)
                    }
                }
                .frame(minHeight: 44)
            } else if isEnabled, let session {
                LabeledContent("Time Remaining") {
                    Text(
                        timerInterval: DeviceDiscoveryTimer.interval(
                            now: now,
                            expiresAt: session.expiresAt
                        ),
                        countsDown: true
                    )
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Discovery")
        } footer: {
            Text("Discovery closes automatically after ten minutes. Turn it off sooner when you finish adding devices.")
        }
    }
}

private struct DeviceDiscoveryResultsSection: View {
    let isEnabled: Bool
    let loadState: DeviceDiscoveryLoadState
    let discoveries: [DiscoveredDevice]
    let decisionDeviceID: UUID?
    let retry: () -> Void
    let select: (DiscoveredDevice) -> Void

    var body: some View {
        Section {
            if !isEnabled {
                Label("Turn on Device Discovery to add a modem.", systemImage: "antenna.radiowaves.left.and.right.slash")
                    .foregroundStyle(.secondary)
            } else {
                DiscoveryResultContent(
                    loadState: loadState,
                    discoveries: discoveries,
                    decisionDeviceID: decisionDeviceID,
                    retry: retry,
                    select: select
                )
            }
        } header: {
            Text("Nearby Devices")
        } footer: {
            if isEnabled {
                Text("Verify the UUID and fingerprint before allowing a device. Editable device names are not used as proof of identity.")
            }
        }
    }
}

private struct DiscoveryResultContent: View {
    let loadState: DeviceDiscoveryLoadState
    let discoveries: [DiscoveredDevice]
    let decisionDeviceID: UUID?
    let retry: () -> Void
    let select: (DiscoveredDevice) -> Void

    var body: some View {
        VStack {
            switch loadState {
            case .loading:
                HStack {
                    ProgressView()
                    Text("Looking for devices…")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
            case .failed(let message):
                VStack(alignment: .leading, spacing: 8) {
                    Label(message, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                    Button {
                        retry()
                    } label: {
                        Text("Try Again")
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            case .loaded:
                if discoveries.isEmpty {
                    Label("No devices found yet", systemImage: "simcard")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                } else {
                    ForEach(discoveries) { candidate in
                        DiscoveredDeviceRow(
                            candidate: candidate,
                            isWorking: decisionDeviceID == candidate.id,
                            select: { select(candidate) }
                        )
                    }
                }
            }
        }
    }
}

private struct DiscoveredDeviceRow: View {
    let candidate: DiscoveredDevice
    let isWorking: Bool
    let select: () -> Void

    var body: some View {
        Button {
            select()
        } label: {
            HStack(spacing: 12) {
                Image(systemName: "simcard")
                    .font(.title2)
                    .foregroundStyle(.tint)
                    .frame(width: 32)
                VStack(alignment: .leading, spacing: 3) {
                    Text("New SIM Device")
                        .foregroundStyle(.primary)
                    Text(candidate.shortIdentifier)
                        .font(.subheadline.monospaced())
                        .foregroundStyle(.secondary)
                    HStack(spacing: 6) {
                        Text(candidate.identityFingerprint)
                        Text(candidate.firstSeenAt, format: .dateTime.hour().minute())
                    }
                    .font(.caption.monospaced())
                    .foregroundStyle(.tertiary)
                }
                Spacer(minLength: 8)
                if isWorking {
                    ProgressView()
                } else {
                    Image(systemName: "chevron.right")
                        .font(.caption.bold())
                        .foregroundStyle(.tertiary)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(isWorking)
        .accessibilityLabel("New SIM device, UUID \(candidate.deviceUUID.uuidString), fingerprint \(candidate.identityFingerprint)")
        .accessibilityHint("Double-tap to review this device.")
    }
}

private struct DeviceDiscoveryLinuxSection: View {
    var body: some View {
        Section("Linux Device") {
            Label("Use unique device mappings and a unique state volume.", systemImage: "externaldrive.badge.checkmark")
            Label("Start the container after discovery is enabled.", systemImage: "play.circle")
            Label("Compare the UUID and fingerprint, then tap Allow.", systemImage: "checkmark.shield")
        }
    }
}

#if DEBUG
@MainActor
private final class PreviewDeviceDiscoveryAPI: DeviceDiscoveryAPI {
    var state: DeviceDiscoverySessionState
    var devices: [DiscoveredDevice]

    init(state: DeviceDiscoverySessionState, devices: [DiscoveredDevice]) {
        self.state = state
        self.devices = devices
    }

    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState { state }
    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState { state }
    func closeDeviceDiscovery() async throws {}
    func discoveredDevices() async throws -> [DiscoveredDevice] { devices }
    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice {
        ApprovedDevice(deviceUUID: id, numericID: 2)
    }
    func rejectDiscoveredDevice(id: UUID) async throws {}
}

private extension DeviceDiscoveryViewModel {
    static func preview(
        enabled: Bool = true,
        devices: [DiscoveredDevice] = PreviewFixtures.discoveredDevices,
        loadState: DeviceDiscoveryLoadState = .loaded
    ) -> DeviceDiscoveryViewModel {
        let session = DeviceDiscoverySession(
            openedAt: PreviewFixtures.now,
            expiresAt: PreviewFixtures.now.addingTimeInterval(600),
            closedAt: nil
        )
        let state = DeviceDiscoverySessionState(active: enabled, session: session)
        return DeviceDiscoveryViewModel(
            api: PreviewDeviceDiscoveryAPI(state: state, devices: devices),
            clock: FixedDeviceDiscoveryClock(now: PreviewFixtures.now),
            isDiscoveryEnabled: enabled,
            session: session,
            discoveries: devices,
            loadState: loadState,
            automaticallyRefreshes: false
        )
    }
}

private extension PreviewFixtures {
    static let discoveredDevices = [
        DiscoveredDevice(
            deviceUUID: UUID(uuidString: "01900000-0000-7000-8000-000000000401")!,
            identityPublicKey: Data(repeating: 4, count: 32),
            commandPublicKey: Data(repeating: 5, count: 32),
            status: .pending,
            firstSeenAt: now.addingTimeInterval(15),
            lastSeenAt: now.addingTimeInterval(30),
            expiresAt: now.addingTimeInterval(600)
        ),
        DiscoveredDevice(
            deviceUUID: UUID(uuidString: "01900000-0000-7000-8000-000000000402")!,
            identityPublicKey: Data(repeating: 8, count: 32),
            commandPublicKey: Data(repeating: 9, count: 32),
            status: .pending,
            firstSeenAt: now.addingTimeInterval(20),
            lastSeenAt: now.addingTimeInterval(35),
            expiresAt: now.addingTimeInterval(600)
        ),
    ]
}

#Preview("Device Discovery") {
    AddDeviceScreen(model: .preview(selectedTab: .profile), discovery: .preview())
}

#Preview("Device Discovery - Empty") {
    AddDeviceScreen(
        model: .preview(selectedTab: .profile),
        discovery: .preview(devices: [])
    )
    .preferredColorScheme(.dark)
}

#Preview("Device Discovery - Error, Chinese") {
    AddDeviceScreen(
        model: .preview(selectedTab: .profile),
        discovery: .preview(
            loadState: .failed(
                String(
                    localized: "The server request failed.",
                    locale: Locale(identifier: "zh-Hans")
                )
            )
        )
    )
    .environment(\.locale, Locale(identifier: "zh-Hans"))
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Device Discovery - Off") {
    AddDeviceScreen(
        model: .preview(selectedTab: .profile),
        discovery: .preview(enabled: false, devices: [])
    )
}
#endif
