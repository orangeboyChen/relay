// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct ESIMManagementScreen: View {
    let model: AppModel
    let line: DeviceLineIdentity

    @State private var profiles: [ESIMProfile] = []
    @State private var isLoading = true
    @State private var isAddingProfile = false
    @State private var errorDescription: String?

    var body: some View {
        Group {
            if isLoading {
                ProgressView("Loading profiles…")
            } else if profiles.isEmpty {
                ContentUnavailableView(
                    "No eSIM Profiles",
                    systemImage: "simcard",
                    description: Text("Add a profile by scanning a QR code or entering its activation code.")
                )
            } else {
                List {
                    Section {
                        ForEach(profiles) { profile in
                            NavigationLink {
                                ESIMProfileDetailScreen(
                                    model: model,
                                    line: line,
                                    profile: profile,
                                    onChange: { updated in
                                        replace(updated)
                                    },
                                    onDelete: {
                                        profiles.removeAll { $0.id == profile.id }
                                    }
                                )
                            } label: {
                                ESIMProfileRow(profile: profile)
                            }
                        }
                    } footer: {
                        Text("Only installed profiles are shown. A profile can be enabled on one eSIM at a time.")
                    }
                }
            }
        }
        .navigationTitle("eSIM Management")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    isAddingProfile = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add eSIM profile")
            }
        }
        .alert("eSIM Unavailable", isPresented: Binding(
            get: { errorDescription != nil },
            set: { if !$0 { errorDescription = nil } }
        )) {
            Button("OK", role: .cancel) { errorDescription = nil }
        } message: {
            Text(errorDescription ?? "")
        }
        .sheet(isPresented: $isAddingProfile) {
            ESIMAddProfileSheet(model: model, line: line) { profile in
                profiles.append(profile)
            }
            .presentationDetents([.medium, .large])
        }
        .task { await loadProfiles() }
        .refreshable { await loadProfiles() }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private func loadProfiles() async {
        isLoading = true
        do {
            profiles = try await model.loadESIMProfiles(line: line).filter {
                !$0.profileID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    || !$0.iccid.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    || !$0.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            }
            errorDescription = nil
        } catch {
            errorDescription = error.localizedDescription
        }
        isLoading = false
    }

    private func replace(_ profile: ESIMProfile) {
        guard let index = profiles.firstIndex(where: { $0.id == profile.id }) else { return }
        profiles[index] = profile
    }
}

private struct ESIMProfileRow: View {
    let profile: ESIMProfile

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: profile.isEnabled ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(profile.isEnabled ? .green : .secondary)
                .font(.title3)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 3) {
                Text(profile.displayName)
                    .font(.body)
                    .lineLimit(1)
                Text(profile.detailSubtitle)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Text(profile.isEnabled ? "Enabled" : "Off")
                .font(.subheadline)
                .foregroundStyle(profile.isEnabled ? .green : .secondary)
        }
        .frame(minHeight: 44)
    }
}

private struct ESIMProfileDetailScreen: View {
    let model: AppModel
    let line: DeviceLineIdentity
    let onChange: (ESIMProfile) -> Void
    let onDelete: () -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var profile: ESIMProfile
    @State private var draftName: String
    @State private var isWorking = false
    @State private var isConfirmingDelete = false
    @State private var errorDescription: String?

    init(
        model: AppModel,
        line: DeviceLineIdentity,
        profile: ESIMProfile,
        onChange: @escaping (ESIMProfile) -> Void,
        onDelete: @escaping () -> Void
    ) {
        self.model = model
        self.line = line
        self.onChange = onChange
        self.onDelete = onDelete
        _profile = State(initialValue: profile)
        _draftName = State(initialValue: profile.name)
    }

    var body: some View {
        Form {
            Section("Profile") {
                Toggle("Enabled", isOn: Binding(
                    get: { profile.isEnabled },
                    set: { updateEnabled($0) }
                ))
                TextField("Profile name", text: $draftName)
                    .textInputAutocapitalization(.words)
                LabeledContent("Profile ID", value: profile.profileID)
                if !profile.iccid.isEmpty {
                    LabeledContent("ICCID", value: profile.iccid)
                }
                if !profile.operatorName.isEmpty {
                    LabeledContent("Operator", value: profile.operatorName)
                }
            }

            if let errorDescription {
                Section {
                    Label(errorDescription, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }

            Section {
                Button("Save Name") { saveName() }
                    .disabled(isWorking || draftName == profile.name)
                Button("Delete Profile", role: .destructive) {
                    isConfirmingDelete = true
                }
                .disabled(isWorking)
            }
        }
        .navigationTitle(profile.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            "Delete this eSIM profile?",
            isPresented: $isConfirmingDelete,
            titleVisibility: .visible
        ) {
            Button("Delete Profile", role: .destructive) { deleteProfile() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This removes the profile from the eSIM. It cannot be restored from Sim Deck.")
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private func updateEnabled(_ enabled: Bool) {
        guard !isWorking else { return }
        isWorking = true
        Task {
            do {
                let updated = try await model.setESIMProfileEnabled(enabled, profile: profile, line: line)
                profile = updated
                onChange(updated)
                errorDescription = nil
            } catch {
                errorDescription = error.localizedDescription
            }
            isWorking = false
        }
    }

    private func saveName() {
        guard !isWorking else { return }
        isWorking = true
        Task {
            do {
                let updated = try await model.renameESIMProfile(draftName, profile: profile, line: line)
                profile = updated
                draftName = updated.name
                onChange(updated)
                errorDescription = nil
            } catch {
                errorDescription = error.localizedDescription
            }
            isWorking = false
        }
    }

    private func deleteProfile() {
        guard !isWorking else { return }
        isWorking = true
        Task {
            do {
                try await model.deleteESIMProfile(profile: profile, line: line)
                onDelete()
                dismiss()
            } catch {
                errorDescription = error.localizedDescription
                isWorking = false
            }
        }
    }
}

private struct ESIMAddProfileSheet: View {
    enum EntryMethod: String, CaseIterable, Identifiable {
        case scan
        case manual

        var id: Self { self }

        var title: LocalizedStringKey {
            switch self {
            case .scan: "Scan QR Code"
            case .manual: "Enter Manually"
            }
        }
    }

    let model: AppModel
    let line: DeviceLineIdentity
    let onAdd: (ESIMProfile) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var method: EntryMethod = .scan
    @State private var activationCode = ""
    @State private var isScannerPresented = false
    @State private var isWorking = false
    @State private var errorDescription: String?

    var body: some View {
        NavigationStack {
            Form {
                Picker("Add Profile", selection: $method) {
                    ForEach(EntryMethod.allCases) { method in
                        Text(method.title).tag(method)
                    }
                }
                .pickerStyle(.segmented)

                if method == .scan {
                    Section {
                        Button {
                            isScannerPresented = true
                        } label: {
                            Label("Scan QR Code", systemImage: "qrcode.viewfinder")
                        }
                        Text("Scan the activation QR code provided by your carrier.")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }

                Section("Activation Code") {
                    TextField("LPA:1$…", text: $activationCode, axis: .vertical)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }

                if let errorDescription {
                    Label(errorDescription, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }
            .navigationTitle("Add eSIM")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") { addProfile() }
                        .disabled(activationCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isWorking)
                }
            }
            .sheet(isPresented: $isScannerPresented) {
                ESIMScannerView { code in
                    activationCode = code
                    method = .manual
                    isScannerPresented = false
                }
                .ignoresSafeArea()
            }
            .scrollEdgeEffectStyle(.soft, for: .all)
        }
    }

    private func addProfile() {
        guard !isWorking else { return }
        isWorking = true
        Task {
            do {
                let profile = try await model.addESIMActivationCode(activationCode, line: line)
                onAdd(profile)
                dismiss()
            } catch {
                errorDescription = error.localizedDescription
                isWorking = false
            }
        }
    }
}

#if canImport(VisionKit)
import VisionKit

private struct ESIMScannerView: UIViewControllerRepresentable {
    let onCode: (String) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(onCode: onCode) }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isPinchToZoomEnabled: true,
            isGuidanceEnabled: true,
            isHighlightingEnabled: true
        )
        scanner.delegate = context.coordinator
        if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
            try? scanner.startScanning()
        }
        return scanner
    }

    func updateUIViewController(_ uiViewController: DataScannerViewController, context: Context) {}

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onCode: (String) -> Void

        init(onCode: @escaping (String) -> Void) {
            self.onCode = onCode
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            didAdd addedItems: [RecognizedItem],
            allItems: [RecognizedItem]
        ) {
            guard case let .barcode(barcode) = addedItems.first,
                  let code = barcode.payloadStringValue,
                  !code.isEmpty else { return }
            onCode(code)
        }
    }
}
#else
private struct ESIMScannerView: View {
    let onCode: (String) -> Void

    var body: some View {
        ContentUnavailableView(
            "Scanner Unavailable",
            systemImage: "qrcode",
            description: Text("Enter the activation code manually on this device.")
        )
    }
}
#endif

#if DEBUG
#Preview("eSIM Management") {
    NavigationStack {
        ESIMManagementScreen(
            model: .preview(devices: [PreviewFixtures.esimLine]),
            line: PreviewFixtures.esimLine
        )
    }
}

#Preview("Add eSIM Profile") {
    ESIMAddProfileSheet(
        model: .preview(devices: [PreviewFixtures.esimLine]),
        line: PreviewFixtures.esimLine,
        onAdd: { _ in }
    )
}
#endif

