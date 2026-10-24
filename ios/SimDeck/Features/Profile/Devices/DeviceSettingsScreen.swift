// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct DeviceSettingsDraft: Equatable {
    var deviceName: String

    init(line: DeviceLineIdentity) {
        deviceName = line.deviceName
    }

    var isValid: Bool { true }
}

struct DeviceSettingsScreen: View {
    let model: AppModel
    let line: DeviceLineIdentity

    @Environment(\.dismiss) private var dismiss
    @State private var draft: DeviceSettingsDraft
    @State private var isWorking = false
    @State private var errorDescription: String?
    @State private var isConfirmingRemoval = false

    private let original: DeviceSettingsDraft

    init(model: AppModel, line: DeviceLineIdentity) {
        self.model = model
        self.line = line
        let draft = DeviceSettingsDraft(line: line)
        original = draft
        _draft = State(initialValue: draft)
    }

    var body: some View {
        Form {
            Section {
                TextField("Device name", text: $draft.deviceName)
                    .textContentType(.name)
            } header: {
                Text("Identity")
            } footer: {
                Text("Names and phone numbers are encrypted on this iPhone before upload.")
            }

            if !model.canEditDeviceProfiles {
                Section {
                    Label(
                        "Restore the encryption key before editing this device.",
                        systemImage: "exclamationmark.triangle.fill"
                    )
                    .foregroundStyle(.orange)
                }
            }

            if let errorDescription {
                Section {
                    Label(errorDescription, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }

            Section {
                Button("Remove Device", role: .destructive) {
                    isConfirmingRemoval = true
                }
                .disabled(isWorking)
            } footer: {
                Text("Removing a device disconnects it immediately. To add it again, turn on Device Discovery and restart the modem container.")
            }
        }
        .navigationTitle("Device")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") {
                    save()
                }
                .disabled(!canSave)
            }
        }
        .confirmationDialog(
            "Remove this device?",
            isPresented: $isConfirmingRemoval,
            titleVisibility: .visible
        ) {
            Button("Remove Device", role: .destructive) {
                remove()
            }
            Button("Cancel", role: .cancel) { }
        } message: {
            Text("The modem credential will stop working immediately. This action cannot be undone.")
        }
        .interactiveDismissDisabled(isWorking)
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private var canSave: Bool {
        draft.isValid && draft != original && !isWorking && model.canEditDeviceProfiles
    }

    private func save() {
        Task {
            isWorking = true
            errorDescription = nil
            do {
                try await model.saveDeviceName(
                    line: line,
                    deviceName: draft.deviceName
                )
                dismiss()
            } catch {
                errorDescription = error.localizedDescription
                isWorking = false
            }
        }
    }

    private func remove() {
        Task {
            isWorking = true
            errorDescription = nil
            do {
                try await model.removeDevice(line: line)
                dismiss()
            } catch {
                errorDescription = error.localizedDescription
                isWorking = false
            }
        }
    }
}

struct SIMSettingsScreen: View {
    let model: AppModel
    let line: DeviceLineIdentity

    @Environment(\.dismiss) private var dismiss
    @State private var numericID: Int
    @State private var lineName: String
    @State private var phoneNumber: String
    @State private var errorDescription: String?
    @State private var controlErrorDescription: String?
    @State private var slotSettings: SlotSettings?
    @State private var originalSlotSettings: SlotSettings?
    @State private var isLoadingControls = true
    @State private var isWorking = false
    @State private var showsVoLTERestartConfirmation = false

    init(model: AppModel, line: DeviceLineIdentity) {
        self.model = model
        self.line = line
        _numericID = State(initialValue: line.numericID)
        _lineName = State(initialValue: line.lineName)
        _phoneNumber = State(initialValue: line.phoneNumber)
    }

    var body: some View {
        Form {
            Section("SIM Card") {
                Stepper(value: $numericID, in: 1...99) {
                    LabeledContent("SIM ID", value: numericID, format: .number)
                }
                TextField("Line name", text: $lineName)
                TextField("Phone number", text: $phoneNumber)
                    .textContentType(.telephoneNumber)
                    .keyboardType(.numbersAndPunctuation)

                if line.simKind == .eSIM {
                    NavigationLink {
                        ESIMManagementScreen(model: model, line: line)
                    } label: {
                        Label("eSIM Management", systemImage: "simcard.2")
                    }
                }
            }
            if let slotSettings {
                SlotControlSection(settings: slotSettingsBinding(for: slotSettings))
            } else if isLoadingControls {
                Section("Slot Controls") {
                    HStack {
                        ProgressView()
                        Text("Loading slot controls…")
                            .foregroundStyle(.secondary)
                    }
                }
            } else if !line.isOnline {
                Section("Slot Controls") {
                    Label("This SIM is offline.", systemImage: "wifi.slash")
                        .foregroundStyle(.secondary)
                }
            }
            if let controlErrorDescription {
                SlotControlErrorSection(message: controlErrorDescription) {
                    Task { await loadSlotSettings() }
                }
            }
            Section("MMS") {
                NavigationLink("MMS Configuration") { MMSSettingsScreen() }
            }
            if let errorDescription {
                Section { Label(errorDescription, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
        }
        .navigationTitle("SIM Card")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                if isWorking {
                    ProgressView()
                } else {
                    Button("Save") {
                        if slotSettings?.volteEnabled != originalSlotSettings?.volteEnabled {
                            showsVoLTERestartConfirmation = true
                        } else {
                            save(restartModem: false)
                        }
                    }
                    .disabled(!canSave)
                }
            }
        }
        .task { await loadSlotSettings() }
        .interactiveDismissDisabled(isWorking)
        .confirmationDialog(
            "Restart Modem to Apply VoLTE?",
            isPresented: $showsVoLTERestartConfirmation,
            titleVisibility: .visible
        ) {
            Button("Save and Restart") { save(restartModem: true) }
            Button("Save Without Restart") { save(restartModem: false) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("VoLTE changes require a modem restart. Restarting temporarily takes this SIM offline.")
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private var canSave: Bool {
        guard model.canEditDeviceProfiles, !isWorking else { return false }
        let identityChanged = numericID != line.numericID
            || lineName != line.lineName
            || phoneNumber != line.phoneNumber
        let controlsChanged = slotSettings != nil && slotSettings != originalSlotSettings
        return identityChanged || controlsChanged
    }

    private func slotSettingsBinding(for value: SlotSettings) -> Binding<SlotSettings> {
        Binding(
            get: { slotSettings ?? value },
            set: { slotSettings = $0 }
        )
    }

    private func loadSlotSettings() async {
        isLoadingControls = line.isOnline
        controlErrorDescription = nil
        guard line.isOnline else { return }
        do {
            let loaded = try await model.loadSlotSettings(line: line)
            slotSettings = loaded
            originalSlotSettings = loaded
        } catch {
            controlErrorDescription = error.localizedDescription
        }
        isLoadingControls = false
    }

    private func save(restartModem: Bool) {
        Task {
            isWorking = true
            errorDescription = nil
            do {
                try await model.saveSIM(line: line, numericID: numericID, lineName: lineName, phoneNumber: phoneNumber)
                if let slotSettings, slotSettings != originalSlotSettings {
                    _ = try await model.saveSlotSettings(slotSettings, line: line)
                    if restartModem {
                        try await model.restartSlotModem(line: line)
                    }
                }
                dismiss()
            } catch {
                errorDescription = error.localizedDescription
                isWorking = false
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
        .scrollEdgeEffectStyle(.soft, for: .all)
    }
}

#if DEBUG
#Preview("Device Settings") {
    NavigationStack {
        DeviceSettingsScreen(
            model: .preview(selectedTab: .profile),
            line: PreviewFixtures.devices[0]
        )
    }
}

#Preview("Device Settings - Large Text") {
    NavigationStack {
        DeviceSettingsScreen(
            model: .preview(selectedTab: .profile),
            line: PreviewFixtures.devices[2]
        )
    }
    .environment(\.dynamicTypeSize, .accessibility3)
}

#Preview("MMS Settings") {
    NavigationStack {
        MMSSettingsScreen()
    }
}

#Preview("SIM Settings") {
    NavigationStack {
        SIMSettingsScreen(
            model: .preview(selectedTab: .profile),
            line: PreviewFixtures.devices[0]
        )
    }
}
#endif
