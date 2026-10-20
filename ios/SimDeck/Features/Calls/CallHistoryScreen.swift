// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct CallHistoryScreen: View {
    let state: CallHistoryState
    let lines: [DeviceLineIdentity]
    let referenceDate: Date
    let callStarter: any CallStarting
    let defaultMessageLineID: UUID?
    let sendMessage: (UUID, String, String) async throws -> Void
    @Binding var editMode: EditMode
    let onDelete: (Set<UUID>) throws -> Void
    let onStarted: (UUID) -> Void

    @State private var startingRecordID: UUID?
    @State private var presentedError: CallHistoryPresentedError?
    @State private var selectedDetail: CallDetailSelection?
    @State private var selectedRecordIDs: Set<UUID> = []
    @State private var isDeleteConfirmationPresented = false
    @State private var filter: CallHistoryFilter = .all
    @State private var selectedLineID: UUID?

    var body: some View {
        Group {
            switch state {
            case .loading:
                ProgressView("Loading calls")
            case .loaded(let records) where filtered(records).isEmpty:
                ContentUnavailableView(
                    filter == .missed ? "No Missed Calls" : "No Calls",
                    systemImage: "phone",
                    description: Text("Calls made through sim-deck appear here.")
                )
            case .loaded(let records):
                List {
                    ForEach(filtered(records)) { record in
                        let linePresentation = record.linePresentation(in: lines)
                        CallHistoryRecordRow(
                            record: record,
                            linePresentation: linePresentation,
                            referenceDate: referenceDate,
                            isEditing: editMode.isEditing,
                            isSelected: selectedRecordIDs.contains(record.id),
                            isStarting: startingRecordID == record.id,
                            isInteractionDisabled: startingRecordID != nil,
                            onSelect: {
                                toggleSelection(for: record.id)
                            },
                            onCall: { startCall(from: record) },
                            onDetails: {
                                selectedDetail = CallDetailSelection(
                                    record: record,
                                    linePresentation: linePresentation
                                )
                            }
                        )
                        .listRowInsets(
                            EdgeInsets(top: 0, leading: 16, bottom: 0, trailing: 8)
                        )
                        .alignmentGuide(.listRowSeparatorLeading) { dimensions in
                            dimensions[.leading] + 62
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            Button(role: .destructive) {
                                deleteRecords(withIDs: [record.id])
                            } label: {
                                Label("Delete", systemImage: "trash")
                            }
                        }
                    }
                }
                .listStyle(.plain)
                .onChange(of: records.map(\.id)) { _, recordIDs in
                    selectedRecordIDs.formIntersection(recordIDs)
                    if records.isEmpty {
                        editMode = .inactive
                    }
                }
            case .failed:
                ContentUnavailableView(
                    "Calls Unavailable",
                    systemImage: "exclamationmark.triangle",
                    description: Text("Check the server connection and try again.")
                )
            }
        }
        .navigationTitle("Recents")
        .navigationBarTitleDisplayMode(.large)
        .onChange(of: editMode) { _, newMode in
            if !newMode.isEditing {
                selectedRecordIDs.removeAll()
            }
        }
        .toolbar {
            if case .loaded(let records) = state, !records.isEmpty {
                ToolbarItem(placement: .topBarLeading) {
                    Button(editMode.isEditing ? "Done" : "Edit") {
                        withAnimation {
                            editMode = editMode.isEditing ? .inactive : .active
                        }
                    }
                    .controlSize(.large)
                }
                ToolbarItem(placement: .principal) {
                    Picker("Call filter", selection: $filter) {
                        Text("All").tag(CallHistoryFilter.all)
                        Text("Missed").tag(CallHistoryFilter.missed)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 140)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button {
                            selectedLineID = nil
                        } label: {
                            if selectedLineID == nil {
                                Label("All SIMs", systemImage: "checkmark")
                            } else {
                                Text("All SIMs")
                            }
                        }

                        ForEach(lines) { line in
                            Button {
                                selectedLineID = line.id
                            } label: {
                                if selectedLineID == line.id {
                                    Label(line.displayName, systemImage: "checkmark")
                                } else {
                                    Text(line.displayName)
                                }
                            }
                        }
                    } label: {
                        Image(systemName: "line.3.horizontal")
                            .font(.body.weight(.semibold))
                    }
                    .buttonStyle(.glass)
                    .buttonBorderShape(.circle)
                    .controlSize(.large)
                    .accessibilityLabel("Filter calls by SIM")
                }
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if editMode.isEditing {
                HStack {
                    Text("\(selectedRecordIDs.count) Selected")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button(role: .destructive) {
                        isDeleteConfirmationPresented = true
                    } label: {
                        Label("Delete", systemImage: "trash")
                            .fontWeight(.semibold)
                    }
                    .disabled(selectedRecordIDs.isEmpty)
                    .frame(minHeight: 44)
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 6)
                .background(.bar)
            }
        }
        .navigationDestination(isPresented: detailIsPresented) {
            if let selectedDetail {
                CallDetailScreen(
                    record: selectedDetail.record,
                    linePresentation: selectedDetail.linePresentation,
                    lines: lines,
                    defaultMessageLineID: defaultMessageLineID,
                    callAction: { startCall(from: selectedDetail.record) },
                    sendAction: sendMessage
                )
            }
        }
        .alert(item: $presentedError) { error in
            Alert(
                title: Text(error.title),
                message: Text(error.message),
                dismissButton: .default(Text("OK"))
            )
        }
        .confirmationDialog(
            "Delete Selected Calls?",
            isPresented: $isDeleteConfirmationPresented,
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                deleteRecords(withIDs: selectedRecordIDs)
            }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func filtered(_ records: [CallRecord]) -> [CallRecord] {
        filter.records(from: records, lineID: selectedLineID)
    }

    private func toggleSelection(for recordID: UUID) {
        if selectedRecordIDs.contains(recordID) {
            selectedRecordIDs.remove(recordID)
        } else {
            selectedRecordIDs.insert(recordID)
        }
    }

    private var detailIsPresented: Binding<Bool> {
        Binding(
            get: { selectedDetail != nil },
            set: { isPresented in
                if !isPresented {
                    selectedDetail = nil
                }
            }
        )
    }

    private func startCall(from record: CallRecord) {
        Task {
            do {
                let plan = try CallHistoryDialPlan(record: record, lines: lines)
                startingRecordID = record.id
                try await callStarter.startCall(to: plan.number, using: plan.lineID)
                onStarted(plan.lineID)
                startingRecordID = nil
            } catch let error as CallHistoryDialPlanError {
                startingRecordID = nil
                presentedError = CallHistoryPresentedError(message: error.message)
            } catch {
                startingRecordID = nil
                presentedError = CallHistoryPresentedError(message: "Could not start the call.")
            }
        }
    }

    private func deleteRecords(withIDs recordIDs: Set<UUID>) {
        do {
            try onDelete(recordIDs)
            selectedRecordIDs.subtract(recordIDs)
            if selectedRecordIDs.isEmpty {
                editMode = .inactive
            }
        } catch {
            presentedError = CallHistoryPresentedError(
                title: "Unable to Delete Calls",
                message: "The call history could not be saved."
            )
        }
    }
}

struct CallHistoryDialPlan: Equatable {
    let number: String
    let lineID: UUID

    init(record: CallRecord, lines: [DeviceLineIdentity]) throws {
        guard let line = lines.first(where: { $0.id == record.lineID }) else {
            throw CallHistoryDialPlanError.lineUnavailable
        }
        guard line.isOnline else {
            throw CallHistoryDialPlanError.lineOffline
        }
        guard DialString.isValid(record.remoteNumber) else {
            throw CallHistoryDialPlanError.invalidNumber
        }

        number = DialString.normalized(record.remoteNumber)
        lineID = line.id
    }
}

enum CallHistoryDialPlanError: Error, Equatable {
    case lineUnavailable
    case lineOffline
    case invalidNumber

    var message: LocalizedStringResource {
        switch self {
        case .lineUnavailable:
            "This SIM is no longer available."
        case .lineOffline:
            "This SIM is offline."
        case .invalidNumber:
            "This call record does not contain a valid phone number."
        }
    }
}

private struct CallHistoryPresentedError: Identifiable {
    let id = UUID()
    var title: LocalizedStringResource = "Unable to Call"
    let message: LocalizedStringResource
}

private struct CallDetailSelection {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
}

private struct CallHistoryRecordRow: View {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
    let referenceDate: Date
    let isEditing: Bool
    let isSelected: Bool
    let isStarting: Bool
    let isInteractionDisabled: Bool
    let onSelect: () -> Void
    let onCall: () -> Void
    let onDetails: () -> Void

    var body: some View {
        HStack(spacing: 0) {
            if isEditing {
                Button(action: onSelect) {
                    HStack(spacing: 12) {
                        Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                            .font(.system(size: 22, weight: .regular))
                            .foregroundStyle(isSelected ? .blue : Color(uiColor: .systemGray3))

                        CallHistoryRow(
                            record: record,
                            linePresentation: linePresentation,
                            referenceDate: referenceDate
                        )
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(record.displayName)
                .accessibilityValue(isSelected ? Text("Selected") : Text("Not selected"))
                .accessibilityHint("Toggles this call for deletion")
            } else {
                Button(action: onCall) {
                    CallHistoryRow(
                        record: record,
                        linePresentation: linePresentation,
                        referenceDate: referenceDate
                    )
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(isInteractionDisabled)
                .accessibilityHint("Calls this number using the displayed SIM")

                if isStarting {
                    ProgressView()
                        .frame(width: 50, height: 50)
                } else {
                    Button(action: onDetails) {
                        Image(systemName: "info.circle")
                            .font(.system(size: 21, weight: .regular))
                            .foregroundStyle(.blue)
                            .frame(width: 44, height: 44)
                            .glassEffect(.regular.interactive(), in: Circle())
                    }
                    .buttonStyle(.plain)
                    .frame(width: 58, height: 50)
                    .accessibilityLabel("Call details")
                }
            }
        }
        .frame(minHeight: 76)
        .offset(y: 3)
    }
}

private struct CallHistoryRow: View {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
    let referenceDate: Date

    var body: some View {
        HStack(spacing: 9) {
            CallHistoryAvatar(record: record)

            VStack(alignment: .leading, spacing: 3) {
                Text(record.displayName)
                    .font(.body.weight(record.result == .missed ? .semibold : .regular))
                    .foregroundStyle(record.result == .missed ? .red : .primary)
                    .lineLimit(1)

                HStack(spacing: 5) {
                    Image(systemName: directionSymbol)
                    SIMBadge(snapshot: linePresentation.snapshot, size: 14)
                    Text(linePresentation.displayName)
                    if let duration = record.duration {
                        Text("·")
                        DurationText(seconds: duration)
                    }
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }

            Spacer(minLength: 8)

            Text(record.startedAt, format: dateFormat)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
    }

    private var directionSymbol: String {
        record.direction == .incoming ? "arrow.down.left" : "arrow.up.right"
    }

    private var dateFormat: Date.FormatStyle {
        if Calendar.current.isDate(record.startedAt, inSameDayAs: referenceDate) {
            return .dateTime.hour().minute()
        }
        return .dateTime.month(.abbreviated).day()
    }
}

enum CallHistoryFilter: Equatable {
    case all
    case missed

    func records(from records: [CallRecord]) -> [CallRecord] {
        switch self {
        case .all:
            records
        case .missed:
            records.filter { $0.result == .missed }
        }
    }

    func records(from records: [CallRecord], lineID: UUID?) -> [CallRecord] {
        let matchingResult = self.records(from: records)
        guard let lineID else { return matchingResult }
        return matchingResult.filter { $0.lineID == lineID }
    }
}

private struct CallHistoryAvatar: View {
    let record: CallRecord

    var body: some View {
        ZStack {
            Circle()
                .fill(
                    LinearGradient(
                        colors: [.blue.opacity(0.32), .indigo.opacity(0.52)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
            if let initials {
                Text(initials)
                    .font(.system(size: 18, weight: .medium, design: .rounded))
                    .foregroundStyle(.white)
            } else {
                Image(systemName: "person.fill")
                    .font(.system(size: 25))
                    .foregroundStyle(.white)
            }
        }
        .frame(width: 46, height: 46)
        .accessibilityHidden(true)
    }

    private var initials: String? {
        guard let name = record.contactName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !name.isEmpty else { return nil }
        let components = name.split(whereSeparator: \.isWhitespace)
        if components.isEmpty { return String(name.prefix(1)).uppercased() }
        return components.prefix(2).compactMap(\.first).map(String.init).joined().uppercased()
    }
}

struct DurationText: View {
    let seconds: TimeInterval

    var body: some View {
        Text(verbatim: CallDurationFormatter.string(elapsed: seconds))
            .monospacedDigit()
    }
}

#if DEBUG
#Preview("Call history") {
    NavigationStack {
        CallHistoryScreen(
            state: .loaded(PreviewFixtures.calls),
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now,
            callStarter: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            defaultMessageLineID: PreviewFixtures.devices.first?.id,
            sendMessage: { _, _, _ in },
            editMode: .constant(.inactive),
            onDelete: { _ in },
            onStarted: { _ in }
        )
    }
}

#Preview("Call history - Empty") {
    NavigationStack {
        CallHistoryScreen(
            state: .loaded([]),
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now,
            callStarter: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            defaultMessageLineID: nil,
            sendMessage: { _, _, _ in },
            editMode: .constant(.inactive),
            onDelete: { _ in },
            onStarted: { _ in }
        )
    }
}

#Preview("Call history - Loading") {
    NavigationStack {
        CallHistoryScreen(
            state: .loading,
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now,
            callStarter: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            defaultMessageLineID: nil,
            sendMessage: { _, _, _ in },
            editMode: .constant(.inactive),
            onDelete: { _ in },
            onStarted: { _ in }
        )
    }
}

#Preview("Call history - Offline") {
    NavigationStack {
        CallHistoryScreen(
            state: .failed,
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now,
            callStarter: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            defaultMessageLineID: nil,
            sendMessage: { _, _, _ in },
            editMode: .constant(.inactive),
            onDelete: { _ in },
            onStarted: { _ in }
        )
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Call history - Removed SIM") {
    NavigationStack {
        CallHistoryScreen(
            state: .loaded([PreviewFixtures.calls[0]]),
            lines: [],
            referenceDate: PreviewFixtures.now,
            callStarter: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            defaultMessageLineID: nil,
            sendMessage: { _, _, _ in },
            editMode: .constant(.inactive),
            onDelete: { _ in },
            onStarted: { _ in }
        )
    }
}

#endif
