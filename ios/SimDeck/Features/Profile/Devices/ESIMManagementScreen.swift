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
                ContentUnavailableView {
                    Label("No eSIM Profiles", systemImage: "simcard.2")
                } description: {
                    Text("Add a cellular plan with a QR code or an activation code from your carrier.")
                } actions: {
                    Button("Add eSIM", systemImage: "plus") {
                        isAddingProfile = true
                    }
                    .buttonStyle(.borderedProminent)
                }
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
                    } header: {
                        Text("Cellular Plans")
                    } footer: {
                        Text("Turn on a plan to use it for calls, messages, or data. Only one plan can be active on this line.")
                    }

                    Section {
                        Button {
                            isAddingProfile = true
                        } label: {
                            Label("Add eSIM", systemImage: "plus")
                        }
                    }
                }
                .listStyle(.insetGrouped)
            }
        }
        .navigationTitle("eSIM")
        .navigationBarTitleDisplayMode(.inline)
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
            .presentationDetents([.large])
            .presentationDragIndicator(.visible)
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
            Image(systemName: "simcard.2")
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(.tint)
                .font(.title3)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 3) {
                Text(profile.displayName)
                    .font(.body)
                    .lineLimit(1)
                Text(profile.operatorName.isEmpty ? profile.detailSubtitle : profile.operatorName)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Text(profile.isEnabled ? "On" : "Off")
                .font(.subheadline)
                .foregroundStyle(profile.isEnabled ? .green : .secondary)
        }
        .frame(minHeight: 48)
        .accessibilityElement(children: .combine)
        .accessibilityValue(profile.isEnabled ? "On" : "Off")
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
            Section("Cellular Plan") {
                Toggle("Turn On This Line", isOn: Binding(
                    get: { profile.isEnabled },
                    set: { updateEnabled($0) }
                ))
                TextField("Cellular Plan Label", text: $draftName)
                    .textInputAutocapitalization(.words)
                    .autocorrectionDisabled()
                    .textContentType(.name)
            }

            Section("SIM Information") {
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
                Button("Delete eSIM", role: .destructive) {
                    isConfirmingDelete = true
                }
                .disabled(isWorking)
            }
        }
        .navigationTitle(profile.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { saveName() }
                    .disabled(isWorking || draftName == profile.name)
            }
        }
        .confirmationDialog(
            "Delete this eSIM?",
            isPresented: $isConfirmingDelete,
            titleVisibility: .visible
        ) {
            Button("Delete eSIM", role: .destructive) { deleteProfile() }
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
            List {
                Section("Add Cellular Plan") {
                    Button {
                        method = .scan
                        isScannerPresented = true
                    } label: {
                        Label("Use QR Code", systemImage: "qrcode.viewfinder")
                    }

                    Button {
                        method = .manual
                    } label: {
                        HStack {
                            Label("Enter Details Manually", systemImage: "keyboard")
                            Spacer()
                            if method == .manual {
                                Image(systemName: "checkmark")
                                    .foregroundStyle(.tint)
                                    .accessibilityHidden(true)
                            }
                        }
                    }
                }

                if method == .manual || !activationCode.isEmpty {
                    Section {
                        TextField("LPA:1$…", text: $activationCode, axis: .vertical)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .lineLimit(1...3)
                    } header: {
                        Text("Activation Code")
                    } footer: {
                        Text("Enter the activation code provided by your carrier.")
                    }
                }

                if let errorDescription {
                    Section {
                        Label(errorDescription, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }
            }
            .listStyle(.insetGrouped)
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

