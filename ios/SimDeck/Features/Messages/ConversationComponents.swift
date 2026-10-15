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
            HStack(spacing: 8) {
                MessageContactAvatar(
                    displayName: conversation.displayName,
                    remoteNumber: conversation.remoteNumber,
                    diameter: 30
                )

                VStack(alignment: .leading, spacing: 1) {
                    Text(conversation.displayName)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)

                    HStack(spacing: 4) {
                        SIMBadge(line: line, size: 14)
                        Text(line.displayName)
                            .lineLimit(1)
                    }
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                }

                Image(systemName: "chevron.down")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .buttonStyle(.plain)
    }

    private func lineFor(_ conversation: MessageConversation) -> DeviceLineIdentity {
        lines.first(where: { $0.id == conversation.lineID }) ?? line
    }
}

struct MessageBubble: View {
    let message: MessageItem
    let line: DeviceLineIdentity
    let openAttachment: () -> Void

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
                .buttonStyle(.plain)
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
