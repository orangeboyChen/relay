import SwiftUI
import UIKit

struct ConversationScreen: View {
    let conversations: [MessageConversation]
    let lines: [DeviceLineIdentity]

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var selectedID: ConversationID
    @State private var draft = ""
    @State private var replyTarget: MessageItem?
    @State private var activeSheet: ConversationSheet?
    @State private var messagePendingDeletion: MessageItem?
    @State private var deletedMessageIDs: Set<UUID> = []

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
                    ForEach(visibleMessages) { message in
                        MessageBubble(
                            message: message,
                            line: line,
                            openAttachment: {
                                guard let attachment = message.attachment else { return }
                                activeSheet = .attachment(attachment)
                            },
                            replyAction: {
                                withAnimation(motionAnimation) {
                                    replyTarget = message
                                }
                            },
                            copyAction: {
                                UIPasteboard.general.string = message.body
                            },
                            forwardAction: {
                                activeSheet = .forward(message)
                            },
                            detailsAction: {
                                activeSheet = .details(message)
                            },
                            deleteAction: {
                                messagePendingDeletion = message
                            }
                        )
                        .id(message.id)
                    }
                }
                .padding()
                .id(selectedID)
                .transition(.opacity)
            }
            .onChange(of: conversation.messages.count, initial: true) {
                scrollToBottom(using: proxy)
            }
            .onChange(of: dynamicTypeSize) {
                scrollToBottom(using: proxy)
            }
            .onChange(of: selectedID) {
                replyTarget = nil
                messagePendingDeletion = nil
                scrollToBottom(using: proxy, animated: true)
            }
        }
        .safeAreaInset(edge: .bottom) {
            MessageComposer(
                draft: $draft,
                replyPreview: replyTarget.map(messagePreview),
                cancelReplyAction: {
                    withAnimation(motionAnimation) {
                        replyTarget = nil
                    }
                },
                sendAction: {
                    withAnimation(motionAnimation) {
                        draft = ""
                        replyTarget = nil
                    }
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
                    selectedID: animatedSelectedID
                )
            }
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case let .attachment(attachment):
                AttachmentPreview(attachment: attachment)
            case let .forward(message):
                NewMessageScreen(
                    lines: lines,
                    selectedLineID: line.id,
                    message: message.body
                )
            case let .details(message):
                MessageDetailsScreen(message: message, line: line)
            }
        }
        .alert(
            "Delete Message?",
            isPresented: deletionConfirmationIsPresented
        ) {
            Button("Delete", role: .destructive) {
                guard let messagePendingDeletion else { return }
                withAnimation(motionAnimation) {
                    deletedMessageIDs.insert(messagePendingDeletion.id)
                    if replyTarget?.id == messagePendingDeletion.id {
                        replyTarget = nil
                    }
                }
                self.messagePendingDeletion = nil
            }
            Button("Cancel", role: .cancel) {
                messagePendingDeletion = nil
            }
        } message: {
            Text("This message will be removed from this conversation.")
        }
    }

    private var conversation: MessageConversation {
        conversations.first(where: { $0.id == selectedID }) ?? conversations[0]
    }

    private var line: DeviceLineIdentity {
        lines.first(where: { $0.id == conversation.lineID }) ?? lines[0]
    }

    private var visibleMessages: [MessageItem] {
        conversation.messages.filter { !deletedMessageIDs.contains($0.id) }
    }

    private var deletionConfirmationIsPresented: Binding<Bool> {
        Binding(
            get: { messagePendingDeletion != nil },
            set: { isPresented in
                if !isPresented {
                    messagePendingDeletion = nil
                }
            }
        )
    }

    private var animatedSelectedID: Binding<ConversationID> {
        Binding(
            get: { selectedID },
            set: { newValue in
                withAnimation(motionAnimation) {
                    selectedID = newValue
                }
            }
        )
    }

    private var motionAnimation: Animation? {
        reduceMotion ? nil : .snappy
    }

    private func messagePreview(_ message: MessageItem) -> String {
        if !message.body.isEmpty {
            return message.body
        }
        return String(localized: "Image")
    }

    private func scrollToBottom(using proxy: ScrollViewProxy, animated: Bool = false) {
        guard let lastID = visibleMessages.last?.id else { return }

        Task { @MainActor in
            await Task.yield()
            if animated {
                withAnimation(motionAnimation) {
                    proxy.scrollTo(lastID, anchor: .bottom)
                }
            } else {
                proxy.scrollTo(lastID, anchor: .bottom)
            }
        }
    }
}

private enum ConversationSheet: Identifiable {
    case attachment(MessageImageAttachment)
    case forward(MessageItem)
    case details(MessageItem)

    var id: String {
        switch self {
        case let .attachment(attachment):
            "attachment-\(attachment.id)"
        case let .forward(message):
            "forward-\(message.id)"
        case let .details(message):
            "details-\(message.id)"
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
