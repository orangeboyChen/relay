// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI

struct CallHistoryScreen: View {
    let state: CallHistoryState
    let lines: [DeviceLineIdentity]
    var contacts: [ContactRecord] = []
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
                let contactIndex = ContactRecordIndex(contacts: contacts)
                List {
                    ForEach(CallHistoryEntry.grouped(filtered(records))) { entry in
                        let record = entry.latestRecord
                        let linePresentation = record.linePresentation(in: lines)
                        let contact = contactIndex.contact(for: record.remoteNumber)
                        CallHistoryRecordRow(
                            entry: entry,
                            linePresentation: linePresentation,
                            contact: contact,
                            referenceDate: referenceDate,
                            isEditing: editMode.isEditing,
                            isSelected: entry.recordIDs.isSubset(of: selectedRecordIDs),
                            isStarting: startingRecordID == record.id,
                            isInteractionDisabled: startingRecordID != nil,
                            onSelect: {
                                toggleSelection(for: entry.recordIDs)
                            },
                            onCall: { startCall(from: record) },
                            onDetails: {
                                selectedDetail = CallDetailSelection(
                                    record: record,
                                    linePresentation: linePresentation,
                                    contact: contact
                                )
                            }
                        )
                        .listRowInsets(
                            EdgeInsets(top: 0, leading: 12, bottom: 0, trailing: 10)
                        )
                        .alignmentGuide(.listRowSeparatorLeading) { dimensions in
                            dimensions[.leading] + 66
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            Button(role: .destructive) {
                                deleteRecords(withIDs: entry.recordIDs)
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
                .onChange(of: filter) { _, _ in
                    pruneSelection(from: records)
                }
                .onChange(of: selectedLineID) { _, _ in
                    pruneSelection(from: records)
                }
            case .failed:
                ContentUnavailableView(
                    "Calls Unavailable",
                    systemImage: "exclamationmark.triangle",
                    description: Text("Check the server connection and try again.")
                )
            }
        }
        .navigationTitle("Calls")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(editMode.isEditing ? .hidden : .visible, for: .tabBar)
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
                    .buttonStyle(.glass)
                    .buttonBorderShape(.capsule)
                    .controlSize(.large)
                }
                if !editMode.isEditing {
                    ToolbarItem(placement: .topBarTrailing) {
                        Menu {
                            Button {
                                filter = .all
                            } label: {
                                if filter == .all {
                                    Label("All", systemImage: "checkmark")
                                } else {
                                    Text("All")
                                }
                            }
                            Button {
                                filter = .missed
                            } label: {
                                if filter == .missed {
                                    Label("Missed", systemImage: "checkmark")
                                } else {
                                    Text("Missed")
                                }
                            }

                            Divider()

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
                            ZStack(alignment: .topTrailing) {
                                Image(systemName: "line.3.horizontal.decrease")
                                    .font(.body.weight(.semibold))
                                    .frame(width: 44, height: 44)
                                    .contentShape(Circle())

                                if activeFilterCount > 0 {
                                    Text(activeFilterCount, format: .number)
                                        .font(.caption2.weight(.bold))
                                        .foregroundStyle(.white)
                                        .frame(minWidth: 20, minHeight: 20)
                                        .background(.blue, in: Circle())
                                        .offset(x: 4, y: -4)
                                }
                            }
                        }
                        .buttonStyle(.glass)
                        .buttonBorderShape(.circle)
                        .controlSize(.large)
                        .accessibilityLabel("Filter calls by SIM")
                    }
                }
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if editMode.isEditing {
                HStack {
                    Text("\(selectedEntryCount) Selected")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button(role: .destructive) {
                        isDeleteConfirmationPresented = true
                    } label: {
                        Label("Delete", systemImage: "trash")
                            .fontWeight(.semibold)
                            .foregroundStyle(
                                selectedRecordIDs.isEmpty
                                    ? AnyShapeStyle(.secondary)
                                    : AnyShapeStyle(.red)
                            )
                            .frame(minHeight: 44)
                            .padding(.horizontal, 12)
                            .contentShape(Capsule())
                    }
                    .buttonStyle(.glass)
                    .buttonBorderShape(.capsule)
                    .disabled(selectedRecordIDs.isEmpty)
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 6)
                .background(.bar)
            }
        }
        .navigationDestination(isPresented: detailIsPresented) {
            if let selectedDetail {
                if let contact = selectedDetail.contact {
                    ContactDetailScreen(
                        contact: contact,
                        lines: lines,
                        defaultMessageLineID: selectedDetail.record.lineID,
                        callAction: { number in
                            await startCall(
                                to: number,
                                using: selectedDetail.record.lineID,
                                recordID: selectedDetail.record.id
                            )
                        },
                        sendAction: sendMessage
                    )
                } else {
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

    private var activeFilterCount: Int {
        (filter == .missed ? 1 : 0) + (selectedLineID == nil ? 0 : 1)
    }

    private var selectedEntryCount: Int {
        guard case .loaded(let records) = state else { return 0 }
        return CallHistoryEntry.grouped(filtered(records))
            .filter { $0.recordIDs.isSubset(of: selectedRecordIDs) }
            .count
    }

    private func toggleSelection(for recordIDs: Set<UUID>) {
        if recordIDs.isSubset(of: selectedRecordIDs) {
            selectedRecordIDs.subtract(recordIDs)
        } else {
            selectedRecordIDs.formUnion(recordIDs)
        }
    }

    private func pruneSelection(from records: [CallRecord]) {
        selectedRecordIDs.formIntersection(Set(filtered(records).map(\.id)))
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
            await startCall(
                to: record.remoteNumber,
                using: record.lineID,
                recordID: record.id
            )
        }
    }

    private func startCall(to number: String, using lineID: UUID, recordID: UUID?) async {
        do {
            let plan = try CallHistoryDialPlan(number: number, lineID: lineID, lines: lines)
            startingRecordID = recordID
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
        try self.init(number: record.remoteNumber, lineID: record.lineID, lines: lines)
    }

    init(number: String, lineID: UUID, lines: [DeviceLineIdentity]) throws {
        guard let line = lines.first(where: { $0.id == lineID }) else {
            throw CallHistoryDialPlanError.lineUnavailable
        }
        guard line.isOnline else {
            throw CallHistoryDialPlanError.lineOffline
        }
        guard DialString.isValid(number) else {
            throw CallHistoryDialPlanError.invalidNumber
        }

        self.number = DialString.normalized(number)
        self.lineID = line.id
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

struct CallDetailSelection {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
    let contact: ContactRecord?
}

private struct CallHistoryRecordRow: View {
    let entry: CallHistoryEntry
    let linePresentation: LineHistoryPresentation
    let contact: ContactRecord?
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
                            entry: entry,
                            linePresentation: linePresentation,
                            contact: contact,
                            referenceDate: referenceDate
                        )
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(entry.latestRecord.displayName)
                .accessibilityValue(isSelected ? Text("Selected") : Text("Not selected"))
                .accessibilityHint("Toggles this call for deletion")
            } else {
                Button(action: onDetails) {
                    CallHistoryRow(
                        entry: entry,
                        linePresentation: linePresentation,
                        contact: contact,
                        referenceDate: referenceDate
                    )
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(isInteractionDisabled)
                .accessibilityHint("Call details")

                if isStarting {
                    ProgressView()
                        .frame(width: 52, height: 52)
                } else {
                    Button(action: onCall) {
                        Image(systemName: "phone.fill")
                            .font(.system(size: 20, weight: .semibold))
                            .foregroundStyle(.blue)
                            .frame(width: 52, height: 52)
                            .background(Color(uiColor: .systemGray6), in: Circle())
                    }
                    .buttonStyle(.plain)
                    .frame(width: 62, height: 56)
                    .disabled(isInteractionDisabled)
                    .accessibilityLabel(entry.latestRecord.displayName)
                    .accessibilityHint("Calls this number using the displayed SIM")
                }
            }
        }
        .frame(minHeight: 82)
    }
}

private struct CallHistoryRow: View {
    let entry: CallHistoryEntry
    let linePresentation: LineHistoryPresentation
    let contact: ContactRecord?
    let referenceDate: Date

    var body: some View {
        HStack(spacing: 12) {
            CallHistoryAvatar(record: record, contact: contact)

            VStack(alignment: .leading, spacing: 3) {
                Text(record.displayName)
                    .font(.headline)
                    .foregroundStyle(record.result == .missed ? .red : .primary)
                    .lineLimit(1)

                HStack(spacing: 5) {
                    Image(systemName: directionSymbol)
                    SIMBadge(snapshot: linePresentation.snapshot, size: 14)
                    Text(linePresentation.displayName)
                    if entry.records.count > 1 {
                        Text("(\(entry.records.count))")
                    }
                }
                .font(.body)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }

            Spacer(minLength: 8)

            CallHistoryTimestamp(date: record.startedAt, referenceDate: referenceDate)
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
    }

    private var record: CallRecord {
        entry.latestRecord
    }

    private var directionSymbol: String {
        record.direction == .incoming ? "arrow.down.left" : "arrow.up.right"
    }
}

struct CallHistoryEntry: Identifiable, Equatable {
    var records: [CallRecord]

    var id: UUID { latestRecord.id }
    var latestRecord: CallRecord { records[0] }
    var recordIDs: Set<UUID> { Set(records.map(\.id)) }

    static func grouped(
        _ records: [CallRecord],
        calendar: Calendar = .current
    ) -> [CallHistoryEntry] {
        let sortedRecords = records.sorted { $0.startedAt > $1.startedAt }
        return sortedRecords.reduce(into: []) { entries, record in
            guard let lastEntry = entries.last,
                  lastEntry.canInclude(record, calendar: calendar) else {
                entries.append(CallHistoryEntry(records: [record]))
                return
            }
            entries[entries.count - 1].records.append(record)
        }
    }

    private func canInclude(_ record: CallRecord, calendar: Calendar) -> Bool {
        let latestRecord = latestRecord
        return latestRecord.lineID == record.lineID
            && DialString.normalized(latestRecord.remoteNumber)
                == DialString.normalized(record.remoteNumber)
            && latestRecord.direction == record.direction
            && latestRecord.result == record.result
            && calendar.isDate(latestRecord.startedAt, inSameDayAs: record.startedAt)
    }
}

private struct CallHistoryTimestamp: View {
    let date: Date
    let referenceDate: Date

    private let calendar = Calendar.current

    var body: some View {
        Group {
            if calendar.isDate(date, inSameDayAs: referenceDate) {
                Text(date, format: .dateTime.hour().minute())
            } else if let yesterday = calendar.date(
                byAdding: .day,
                value: -1,
                to: referenceDate
            ), calendar.isDate(date, inSameDayAs: yesterday) {
                Text(verbatim: relativeDayText)
            } else if isInRecentWeek {
                Text(date, format: .dateTime.weekday(.abbreviated))
            } else {
                Text(date, format: .dateTime.month(.abbreviated).day())
            }
        }
        .font(.body)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }

    private var isInRecentWeek: Bool {
        guard let startOfRecentWeek = calendar.date(
            byAdding: .day,
            value: -6,
            to: calendar.startOfDay(for: referenceDate)
        ) else { return false }
        return date >= startOfRecentWeek && date < referenceDate
    }

    private var relativeDayText: String {
        let formatter = RelativeDateTimeFormatter()
        formatter.dateTimeStyle = .named
        return formatter.localizedString(for: date, relativeTo: referenceDate).localizedCapitalized
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
    let contact: ContactRecord?

    var body: some View {
        PersonAvatar(
            imageData: contact?.thumbnailImageData,
            displayName: contact?.displayName ?? record.contactName,
            organizationName: contact?.organizationName,
            size: 54
        )
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
            state: .loaded(CallHistoryPreviewFixtures.records),
            lines: PreviewFixtures.devices,
            contacts: PreviewContactFixtures.contacts,
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

#Preview("Call history - Editing") {
    CallHistoryEditingPreview()
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

private struct CallHistoryEditingPreview: View {
    @State private var editMode: EditMode = .active

    var body: some View {
        TabView {
            Tab("Calls", systemImage: "phone.fill") {
                NavigationStack {
                    CallHistoryScreen(
                        state: .loaded(CallHistoryPreviewFixtures.records),
                        lines: PreviewFixtures.devices,
                        contacts: PreviewContactFixtures.contacts,
                        referenceDate: PreviewFixtures.now,
                        callStarter: CallKitCoordinator(
                            transport: PreviewCallTransport(),
                            integratesWithSystem: false
                        ),
                        defaultMessageLineID: PreviewFixtures.devices.first?.id,
                        sendMessage: { _, _, _ in },
                        editMode: $editMode,
                        onDelete: { _ in },
                        onStarted: { _ in }
                    )
                }
            }

            Tab("Messages", systemImage: "message.fill") {
                Color.clear
            }
        }
        .tabViewStyle(.tabBarOnly)
    }
}

private enum CallHistoryPreviewFixtures {
    static let records = PreviewFixtures.calls + [
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000004")!,
            lineID: PreviewFixtures.devices[0].id,
            lineSnapshot: LineHistorySnapshot(line: PreviewFixtures.devices[0]),
            remoteNumber: "13800000000",
            contactName: "China Mobile",
            direction: .incoming,
            result: .answered,
            startedAt: PreviewFixtures.now.addingTimeInterval(-540),
            endedAt: PreviewFixtures.now.addingTimeInterval(-421)
        ),
    ]
}

#endif
