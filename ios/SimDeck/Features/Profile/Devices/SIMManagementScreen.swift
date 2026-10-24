// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct SIMManagementScreen: View {
    let model: AppModel

    @State private var simToDelete: ManagedSIM?
    @State private var errorDescription: String?

    var body: some View {
        List {
            if !esimLines.isEmpty {
                Section("eSIM") {
                    ForEach(esimLines) { line in
                        NavigationLink {
                            ESIMManagementScreen(model: model, line: line)
                        } label: {
                            Label(
                                "eSIM Management · SIM \(line.numericID)",
                                systemImage: "simcard.2"
                            )
                        }
                    }
                }
            }

            if model.managedSIMs.isEmpty {
                ContentUnavailableView("No SIM Cards", systemImage: "simcard", description: Text("SIM cards appear after a modem reports its ICCID."))
            } else {
                ForEach(model.managedSIMs) { sim in
                    ManagedSIMRow(sim: sim, delete: sim.isOffline ? { simToDelete = sim } : nil)
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            if sim.isOffline {
                                Button("Delete", role: .destructive) { simToDelete = sim }
                            }
                        }
                }
            }
            if let errorDescription {
                Section {
                    Label(errorDescription, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }
        }
        .navigationTitle("SIM Cards")
        .refreshable { await model.refreshSIMCards() }
        .task { await model.refreshSIMCards() }
        .confirmationDialog("Delete this offline SIM?", isPresented: Binding(get: { simToDelete != nil }, set: { if !$0 { simToDelete = nil } }), titleVisibility: .visible, presenting: simToDelete) { sim in
            Button("Delete SIM", role: .destructive) {
                Task {
                    do { try await model.removeSIM(sim) }
                    catch { errorDescription = error.localizedDescription }
                    simToDelete = nil
                }
            }
            Button("Cancel", role: .cancel) { simToDelete = nil }
        } message: { _ in
            Text("The SIM identity and its encrypted profile will be removed from this account. A future insertion creates a new managed record.")
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private var esimLines: [DeviceLineIdentity] {
        model.devices.filter { $0.simKind == .eSIM && $0.simID != nil }
    }
}

private struct ManagedSIMRow: View {
    let sim: ManagedSIM
    let delete: (() -> Void)?

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                SIMLabel(
                    number: sim.numericID,
                    displayName: sim.phoneNumber.isEmpty
                        ? (sim.lineName.isEmpty ? "SIM \(sim.numericID)" : sim.lineName)
                        : sim.phoneNumber,
                    phoneNumber: sim.phoneNumber,
                    lineName: sim.lineName,
                    size: 22
                )
                Text(sim.isOffline ? "Offline" : "Inserted in device")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: sim.isOffline ? "wifi.slash" : "checkmark.circle.fill")
                .foregroundStyle(sim.isOffline ? AnyShapeStyle(.secondary) : AnyShapeStyle(.green))
                .accessibilityHidden(true)
            if let delete {
                Button(role: .destructive, action: delete) {
                    Image(systemName: "trash")
                        .frame(width: 44, height: 44)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Delete SIM")
            }
        }
        .frame(minHeight: 44)
        .accessibilityElement(children: .contain)
    }
}

#if DEBUG
#Preview("SIM Management") {
    NavigationStack { SIMManagementScreen(model: .preview(selectedTab: .profile)) }
}
#endif
