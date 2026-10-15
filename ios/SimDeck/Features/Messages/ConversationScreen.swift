import SwiftUI

struct ConversationScreen: View {
    let conversations: [MessageConversation]
    let lines: [DeviceLineIdentity]

    @State private var selectedID: ConversationID
    @State private var draft = ""
    @State private var previewedAttachment: MessageImageAttachment?

    init(
        initialID: ConversationID,
        conversations: [MessageConversation],
        lines: [DeviceLineIdentity]
    ) {
        self.conversations = conversations
        self.lines = lines
        _selectedID = State(initialValue: initialID)
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
                    ForEach(conversation.messages) { message in
                        MessageBubble(message: message, line: line) {
                            previewedAttachment = message.attachment
                        }
                        .id(message.id)
                    }
                }
                .padding()
            }
            .onChange(of: conversation.messages.count, initial: true) {
                if let lastID = conversation.messages.last?.id {
                    proxy.scrollTo(lastID, anchor: .bottom)
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            MessageComposer(
                draft: $draft,
                sendAction: {
                    draft = ""
                }
            )
        }
        .navigationTitle(conversation.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                ConversationHeader(
                    conversation: conversation,
                    line: line,
                    alternatives: conversations,
                    lines: lines,
                    selectedID: $selectedID
                )
            }
        }
        .sheet(item: $previewedAttachment) { attachment in
            AttachmentPreview(attachment: attachment)
        }
    }

    private var conversation: MessageConversation {
        conversations.first(where: { $0.id == selectedID }) ?? conversations[0]
    }

    private var line: DeviceLineIdentity {
        lines.first(where: { $0.id == conversation.lineID }) ?? lines[0]
    }
}

private struct ConversationHeader: View {
    let conversation: MessageConversation
    let line: DeviceLineIdentity
    let alternatives: [MessageConversation]
    let lines: [DeviceLineIdentity]
    @Binding var selectedID: ConversationID

    var body: some View {
        VStack(spacing: 1) {
            Text(conversation.displayName)
                .font(.headline)
            Menu {
                ForEach(alternatives) { alternative in
                    Button("SIM \(lineFor(alternative).numericID) · \(lineFor(alternative).displayName)") {
                        selectedID = alternative.id
                    }
                }
            } label: {
                HStack(spacing: 5) {
                    SIMBadge(line: line)
                    Text(line.displayName)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func lineFor(_ conversation: MessageConversation) -> DeviceLineIdentity {
        lines.first(where: { $0.id == conversation.lineID }) ?? line
    }
}

private struct MessageBubble: View {
    let message: MessageItem
    let line: DeviceLineIdentity
    let openAttachment: () -> Void

    var body: some View {
        HStack {
            if message.direction == .sent {
                Spacer(minLength: 48)
            }

            VStack(alignment: message.direction == .sent ? .trailing : .leading, spacing: 5) {
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
                .background(
                    message.direction == .sent ? Color.blue : Color(uiColor: .secondarySystemBackground),
                    in: RoundedRectangle(cornerRadius: 18)
                )

                HStack(spacing: 5) {
                    SIMBadge(line: line)
                    Text(message.sentAt, format: .dateTime.hour().minute())
                    Text(deliveryStatus)
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
            }

            if message.direction == .received {
                Spacer(minLength: 48)
            }
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

private struct MessageImage: View {
    let attachment: MessageImageAttachment

    var body: some View {
        Image(systemName: attachment.systemImageName)
            .font(.system(size: 52))
            .foregroundStyle(.white)
            .frame(width: 180, height: 120)
            .background(.orange.gradient, in: RoundedRectangle(cornerRadius: 12))
    }
}

private struct AttachmentPreview: View {
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
#Preview("Conversation") {
    NavigationStack {
        ConversationScreen(
            initialID: PreviewFixtures.conversations[0].id,
            conversations: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices
        )
    }
}
#endif
