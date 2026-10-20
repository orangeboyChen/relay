// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct CallDetailScreen: View {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation

    var body: some View {
        Form {
            Section("Remote") {
                LabeledContent("Contact", value: record.displayName)
                LabeledContent("Number", value: record.remoteNumber)
            }

            Section("Line") {
                LabeledContent("Local number", value: linePresentation.displayName)
                LabeledContent("SIM ID") {
                    SIMBadge(snapshot: linePresentation.snapshot)
                }
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
#Preview("Call detail") {
    NavigationStack {
        CallDetailScreen(
            record: PreviewFixtures.calls[0],
            linePresentation: PreviewFixtures.calls[0].linePresentation(
                in: PreviewFixtures.devices
            )
        )
    }
}
#endif
