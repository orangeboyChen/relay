import SwiftUI

struct CallHistoryScreen: View {
    let state: CallHistoryState
    let lines: [DeviceLineIdentity]
    let referenceDate: Date
    let callStarter: any CallStarting
    let onStarted: (UUID) -> Void

    @State private var startingRecordID: UUID?
    @State private var presentedError: CallHistoryPresentedError?
    @State private var selectedDetail: CallDetailSelection?

    var body: some View {
        Group {
            switch state {
            case .loading:
                ProgressView("Loading calls")
            case .loaded(let records) where records.isEmpty:
                ContentUnavailableView(
                    "No Calls",
                    systemImage: "phone",
                    description: Text("Calls made through sim-deck appear here.")
                )
            case .loaded(let records):
                List(records) { record in
                    let linePresentation = record.linePresentation(in: lines)
                    HStack(spacing: 0) {
                        Button {
                            startCall(from: record)
                        } label: {
                            CallHistoryRow(
                                record: record,
                                linePresentation: linePresentation,
                                referenceDate: referenceDate
                            )
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .disabled(startingRecordID != nil)
                        .accessibilityHint("Calls this number using the displayed SIM")

                        if startingRecordID == record.id {
                            ProgressView()
                                .frame(width: 44, height: 44)
                        } else {
                            Button {
                                selectedDetail = CallDetailSelection(
                                    record: record,
                                    linePresentation: linePresentation
                                )
                            } label: {
                                Image(systemName: "info.circle")
                                    .font(.title3)
                                    .foregroundStyle(.blue)
                                    .frame(width: 44, height: 44)
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Call details")
                        }
                    }
                }
                .listStyle(.plain)
            case .failed:
                ContentUnavailableView(
                    "Calls Unavailable",
                    systemImage: "exclamationmark.triangle",
                    description: Text("Check the server connection and try again.")
                )
            }
        }
        .navigationTitle("Calls")
        .navigationDestination(isPresented: detailIsPresented) {
            if let selectedDetail {
                CallDetailScreen(
                    record: selectedDetail.record,
                    linePresentation: selectedDetail.linePresentation
                )
            }
        }
        .alert(item: $presentedError) { error in
            Alert(
                title: Text("Unable to Call"),
                message: Text(error.message),
                dismissButton: .default(Text("OK"))
            )
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
    let message: LocalizedStringResource
}

private struct CallDetailSelection {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
}

private struct CallHistoryRow: View {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
    let referenceDate: Date

    var body: some View {
        HStack(spacing: 12) {
            SIMBadge(snapshot: linePresentation.snapshot)

            VStack(alignment: .leading, spacing: 3) {
                Text(record.displayName)
                    .font(.body.weight(record.result == .missed ? .semibold : .regular))
                    .foregroundStyle(record.result == .missed ? .red : .primary)
                    .lineLimit(1)

                HStack(spacing: 5) {
                    Label(callKind, systemImage: directionSymbol)
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

    private var callKind: LocalizedStringResource {
        switch record.result {
        case .missed:
            "Missed"
        case .rejected:
            "Rejected"
        case .failed:
            "Failed"
        case .answered where record.direction == .incoming:
            "Incoming"
        case .answered:
            "Outgoing"
        }
    }

    private var directionSymbol: String {
        record.direction == .incoming ? "phone.arrow.down.left" : "phone.arrow.up.right"
    }

    private var dateFormat: Date.FormatStyle {
        if Calendar.current.isDate(record.startedAt, inSameDayAs: referenceDate) {
            return .dateTime.hour().minute()
        }
        return .dateTime.month(.abbreviated).day()
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
            onStarted: { _ in }
        )
    }
}

#endif
