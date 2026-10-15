import SwiftUI

struct ConversationScreen: View {
    let conversations: [MessageConversation]
    let lines: [DeviceLineIdentity]

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

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
                scrollToBottom(using: proxy)
            }
            .onChange(of: dynamicTypeSize) {
                scrollToBottom(using: proxy)
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

    private func scrollToBottom(using proxy: ScrollViewProxy) {
        guard let lastID = conversation.messages.last?.id else { return }

        Task { @MainActor in
            await Task.yield()
            proxy.scrollTo(lastID, anchor: .bottom)
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

#Preview("Conversation - Dark Accessibility") {
    NavigationStack {
        ConversationScreen(
            initialID: PreviewFixtures.conversations[0].id,
            conversations: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices
        )
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Conversation - Simplified Chinese") {
    NavigationStack {
        ConversationScreen(
            initialID: PreviewFixtures.conversations[0].id,
            conversations: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices
        )
    }
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}
#endif
