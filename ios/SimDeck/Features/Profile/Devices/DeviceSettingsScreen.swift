import SwiftUI

struct DeviceSettingsDraft: Equatable {
    var numericID: Int
    var deviceName: String
    var lineName: String
    var phoneNumber: String

    init(line: DeviceLineIdentity) {
        numericID = line.numericID
        deviceName = line.deviceName
        lineName = line.lineName
        phoneNumber = line.phoneNumber
    }

    var isValid: Bool { (1...99).contains(numericID) }
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
                Stepper(value: $draft.numericID, in: 1...99) {
                    LabeledContent("SIM ID", value: draft.numericID, format: .number)
                }
                TextField("Device name", text: $draft.deviceName)
                    .textContentType(.name)
                TextField("Line name", text: $draft.lineName)
                TextField("Phone number", text: $draft.phoneNumber)
                    .textContentType(.telephoneNumber)
                    .keyboardType(.numbersAndPunctuation)
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

            Section("MMS") {
                NavigationLink("MMS Configuration") {
                    MMSSettingsScreen()
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
                try await model.saveDevice(
                    line: line,
                    numericID: draft.numericID,
                    deviceName: draft.deviceName,
                    lineName: draft.lineName,
                    phoneNumber: draft.phoneNumber
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
#endif
