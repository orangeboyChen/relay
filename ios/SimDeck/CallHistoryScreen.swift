import SwiftUI

struct CallHistoryScreen: View {
    let state: CallHistoryState
    let lines: [DeviceLineIdentity]
    let referenceDate: Date

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
                    if let line = lines.first(where: { $0.id == record.lineID }) {
                        NavigationLink {
                            CallDetailScreen(record: record, line: line)
                        } label: {
                            CallHistoryRow(
                                record: record,
                                line: line,
                                referenceDate: referenceDate
                            )
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
    }
}

private struct CallHistoryRow: View {
    let record: CallRecord
    let line: DeviceLineIdentity
    let referenceDate: Date

    var body: some View {
        HStack(spacing: 12) {
            SIMBadge(line: line)

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

private struct DurationText: View {
    let seconds: TimeInterval

    var body: some View {
        let totalSeconds = max(0, Int(seconds.rounded(.down)))
        let minutes = totalSeconds / 60
        let remainingSeconds = totalSeconds % 60

        Text("\(minutes) min \(remainingSeconds) sec")
    }
}

private struct CallDetailScreen: View {
    let record: CallRecord
    let line: DeviceLineIdentity

    var body: some View {
        Form {
            Section("Remote") {
                LabeledContent("Contact", value: record.displayName)
                LabeledContent("Number", value: record.remoteNumber)
            }

            Section("Line") {
                LabeledContent("Local number", value: line.displayName)
                LabeledContent("SIM ID", value: line.numericID.formatted(.number.grouping(.never)))
            }

            Section("Call") {
                LabeledContent("Started") {
                    Text(record.startedAt, format: .dateTime.year().month().day().hour().minute())
                }
                LabeledContent("Ended") {
                    if let endedAt = record.endedAt {
                        Text(endedAt, format: .dateTime.year().month().day().hour().minute())
                    } else {
                        Text("Not available")
                            .foregroundStyle(.secondary)
                    }
                }
                if let duration = record.duration {
                    LabeledContent("Duration") {
                        DurationText(seconds: duration)
                    }
                }
                LabeledContent("Result") {
                    Text(result)
                }
            }
        }
        .navigationTitle(record.displayName)
        .navigationBarTitleDisplayMode(.inline)
    }

    private var result: LocalizedStringResource {
        switch record.result {
        case .answered:
            "Answered"
        case .missed:
            "Missed"
        case .rejected:
            "Rejected"
        case .failed:
            "Failed"
        }
    }
}

#if DEBUG
#Preview("Call history") {
    NavigationStack {
        CallHistoryScreen(
            state: .loaded(PreviewFixtures.calls),
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now
        )
    }
}

#Preview("Call history - Empty") {
    NavigationStack {
        CallHistoryScreen(
            state: .loaded([]),
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now
        )
    }
}

#Preview("Call history - Loading") {
    NavigationStack {
        CallHistoryScreen(
            state: .loading,
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now
        )
    }
}

#Preview("Call history - Offline") {
    NavigationStack {
        CallHistoryScreen(
            state: .failed,
            lines: PreviewFixtures.devices,
            referenceDate: PreviewFixtures.now
        )
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Call detail") {
    NavigationStack {
        CallDetailScreen(
            record: PreviewFixtures.calls[0],
            line: PreviewFixtures.devices[0]
        )
    }
}
#endif
