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
    @State private var isWorking = false

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
                Button("Save") { save() }
                    .disabled(isWorking || !model.canEditDeviceProfiles)
            }
        }
    }

    private func save() {
        Task {
            isWorking = true
            do {
                try await model.saveSIM(line: line, numericID: numericID, lineName: lineName, phoneNumber: phoneNumber)
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
