import SwiftUI

struct MessageContextMenu: View {
    let canCopy: Bool
    let canForward: Bool
    let canRetry: Bool
    let replyAction: () -> Void
    let copyAction: () -> Void
    let forwardAction: () -> Void
    let detailsAction: () -> Void
    let retryAction: () -> Void
    let deleteAction: () -> Void

    var body: some View {
        Button(action: replyAction) {
            Label("Reply", systemImage: "arrowshape.turn.up.left")
        }

        if canCopy {
            Button(action: copyAction) {
                Label("Copy", systemImage: "document.on.document")
            }
        }

        if canForward {
            Button(action: forwardAction) {
                Label("Forward", systemImage: "arrowshape.turn.up.right")
            }
        }

        if canRetry {
            Button(action: retryAction) {
                Label("Try Again", systemImage: "arrow.clockwise")
            }
        }

        Divider()

        Button(action: detailsAction) {
            Label("Details", systemImage: "info.circle")
        }

        Divider()

        Button(role: .destructive, action: deleteAction) {
            Label("Delete", systemImage: "trash")
        }
    }
}

struct MessageDetailsScreen: View {
    let message: MessageItem
    let line: DeviceLineIdentity

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                Section("Message") {
                    LabeledContent("Direction") {
                        Text(direction)
                    }
                    LabeledContent("Time") {
                        Text(message.sentAt, format: .dateTime.year().month().day().hour().minute())
                    }
                    LabeledContent("Status") {
                        Text(deliveryStatus)
                    }
                }

                Section("SIM") {
                    LabeledContent("SIM ID") {
                        SIMBadge(line: line)
                    }
                    LabeledContent("Local number", value: line.displayName)
                }
            }
            .navigationTitle("Message Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }

    private var direction: LocalizedStringResource {
        message.direction == .sent ? "Sent" : "Received"
    }

    private var deliveryStatus: LocalizedStringResource {
        switch message.status {
        case .sending: "Sending"
        case .sent: "Sent"
        case .delivered: "Delivered"
        case .failed: "Failed"
        }
    }
}

#if DEBUG
#Preview("Message context actions") {
    MessageContextMenu(
        canCopy: true,
        canForward: true,
        canRetry: false,
        replyAction: {},
        copyAction: {},
        forwardAction: {},
        detailsAction: {},
        retryAction: {},
        deleteAction: {}
    )
}

#Preview("Message details") {
    MessageDetailsScreen(
        message: PreviewFixtures.conversations[0].messages[1],
        line: PreviewFixtures.devices[0]
    )
}
#endif
