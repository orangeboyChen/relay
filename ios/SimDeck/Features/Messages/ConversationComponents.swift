import SwiftUI

struct ConversationHeader: View {
    let conversation: MessageConversation
    let line: DeviceLineIdentity
    let alternatives: [MessageConversation]
    let lines: [DeviceLineIdentity]
    @Binding var selectedID: ConversationID

    var body: some View {
        Menu {
            ForEach(alternatives) { alternative in
                Button("SIM \(lineFor(alternative).numericID) · \(lineFor(alternative).displayName)") {
                    selectedID = alternative.id
                }
            }
        } label: {
            VStack(spacing: 2) {
                MessageContactAvatar(
                    displayName: conversation.displayName,
                    remoteNumber: conversation.remoteNumber,
                    diameter: 36
                )

                HStack(spacing: 3) {
                    Text(conversation.displayName)
                        .lineLimit(1)

                    Image(systemName: "chevron.down")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(.tertiary)
                }
                .font(.caption.weight(.medium))
                .foregroundStyle(.primary)

                HStack(spacing: 3) {
                    SIMBadge(line: line, size: 12)
                    Text(line.displayName)
                        .lineLimit(1)
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
            }
            .frame(maxWidth: 220)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(
            Text(verbatim: "\(conversation.displayName), SIM \(line.numericID), \(line.displayName)")
        )
    }

    private func lineFor(_ conversation: MessageConversation) -> DeviceLineIdentity {
        lines.first(where: { $0.id == conversation.lineID }) ?? line
    }
}

struct MessageBubble: View {
    let message: MessageItem
    let line: DeviceLineIdentity
    let openAttachment: () -> Void
    let replyAction: () -> Void
    let copyAction: () -> Void
    let forwardAction: () -> Void
    let detailsAction: () -> Void
    let retryAction: () -> Void
    let deleteAction: () -> Void

    init(
        message: MessageItem,
        line: DeviceLineIdentity,
        openAttachment: @escaping () -> Void = {},
        replyAction: @escaping () -> Void = {},
        copyAction: @escaping () -> Void = {},
        forwardAction: @escaping () -> Void = {},
        detailsAction: @escaping () -> Void = {},
        retryAction: @escaping () -> Void = {},
        deleteAction: @escaping () -> Void = {}
    ) {
        self.message = message
        self.line = line
        self.openAttachment = openAttachment
        self.replyAction = replyAction
        self.copyAction = copyAction
        self.forwardAction = forwardAction
        self.detailsAction = detailsAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
    }

    var body: some View {
        HStack {
            if message.direction == .sent {
                Spacer(minLength: 48)
            }

            VStack(alignment: message.direction == .sent ? .trailing : .leading, spacing: 4) {
                bubbleContent
                messageMetadata
            }

            if message.direction == .received {
                Spacer(minLength: 48)
            }
        }
    }

    private var bubbleContent: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let attachment = message.attachment {
                Button(action: openAttachment) {
                    MessageImage(attachment: attachment)
                }
                .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.98))
                .accessibilityLabel("Preview image")
            }
            if !message.body.isEmpty {
                Text(message.body)
                    .foregroundStyle(message.direction == .sent ? .white : .primary)
            }
        }
        .padding(.horizontal, 13)
        .padding(.vertical, 9)
        .background(bubbleColor, in: bubbleShape)
        .contentShape(.contextMenuPreview, bubbleShape)
        .contextMenu {
            MessageContextMenu(
                canCopy: !message.body.isEmpty,
                canForward: !message.body.isEmpty,
                canRetry: message.direction == .sent && message.status == .failed,
                replyAction: replyAction,
                copyAction: copyAction,
                forwardAction: forwardAction,
                detailsAction: detailsAction,
                retryAction: retryAction,
                deleteAction: deleteAction
            )
        } preview: {
            MessageContextPreview(message: message, bubbleColor: bubbleColor)
                .clipShape(bubbleShape)
        }
    }

    private var messageMetadata: some View {
        HStack(spacing: 5) {
            SIMBadge(line: line, size: 14)
            Text(message.sentAt, format: .dateTime.hour().minute())
            Text(deliveryStatus)
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
    }

    private var bubbleColor: Color {
        message.direction == .sent
            ? .blue
            : Color(uiColor: .secondarySystemBackground)
    }

    private var bubbleShape: UnevenRoundedRectangle {
        if message.direction == .sent {
            UnevenRoundedRectangle(
                topLeadingRadius: 18,
                bottomLeadingRadius: 18,
                bottomTrailingRadius: 4,
                topTrailingRadius: 18,
                style: .continuous
            )
        } else {
            UnevenRoundedRectangle(
                topLeadingRadius: 18,
                bottomLeadingRadius: 4,
                bottomTrailingRadius: 18,
                topTrailingRadius: 18,
                style: .continuous
            )
        }
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

private struct MessageContextPreview: View {
    let message: MessageItem
    let bubbleColor: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let attachment = message.attachment {
                MessageImage(attachment: attachment)
            }
            if !message.body.isEmpty {
                Text(message.body)
                    .foregroundStyle(message.direction == .sent ? .white : .primary)
            }
        }
        .padding(.horizontal, 13)
        .padding(.vertical, 9)
        .background(bubbleColor)
    }
}

struct MessageImage: View {
    let attachment: MessageImageAttachment

    var body: some View {
        Image(systemName: attachment.systemImageName)
            .font(.system(size: 52))
            .foregroundStyle(.white)
            .frame(width: 180, height: 120)
            .background(.orange.gradient, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

struct AttachmentPreview: View {
    let attachment: MessageImageAttachment

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            MessageImage(attachment: attachment)
                .scaleEffect(1.5)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(.black)
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done") { dismiss() }
                    }
                }
        }
    }
}

#if DEBUG
#Preview("Conversation components") {
    VStack(spacing: 20) {
        ConversationHeader(
            conversation: PreviewFixtures.conversations[0],
            line: PreviewFixtures.devices[0],
            alternatives: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices,
            selectedID: .constant(PreviewFixtures.conversations[0].id)
        )

        ForEach(PreviewFixtures.conversations[0].messages) { message in
            MessageBubble(message: message, line: PreviewFixtures.devices[0]) {}
        }
    }
    .padding()
}
#endif
