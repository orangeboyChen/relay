// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct ConversationScreen: View {
    let conversations: [MessageConversation]
    let lines: [DeviceLineIdentity]
    let sendAction: (UUID, String, String) async throws -> Void
    let retryAction: (UUID) async throws -> Void
    let deleteAction: (UUID) throws -> Void
    let markReadAction: (ConversationID) -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var selectedID: ConversationID
    @State private var draft = ""
    @State private var replyTarget: MessageItem?
    @State private var activeSheet: ConversationSheet?
    @State private var messagePendingDeletion: MessageItem?
    @State private var timestampRevealState = MessageTimestampRevealState()
    @State private var isSending = false
    @State private var errorMessage: String?

    init(
        initialID: ConversationID,
        conversations: [MessageConversation],
        lines: [DeviceLineIdentity],
        sendAction: @escaping (UUID, String, String) async throws -> Void = { _, _, _ in },
        retryAction: @escaping (UUID) async throws -> Void = { _ in },
        deleteAction: @escaping (UUID) throws -> Void = { _ in },
        markReadAction: @escaping (ConversationID) -> Void = { _ in }
    ) {
        self.conversations = conversations
        self.lines = lines
        self.sendAction = sendAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
        self.markReadAction = markReadAction
        _selectedID = State(initialValue: initialID)
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
                    ForEach(conversation.messages) { message in
                        MessageBubble(
                            message: message,
                            lineSnapshot: linePresentation.snapshot,
                            canRetry: currentLine?.isOnline == true,
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
                            copyVerificationCodeAction: {
                                guard let code = VerificationCodeDetector.code(in: message.body) else { return }
                                UIPasteboard.general.string = code
                            },
                            forwardAction: {
                                activeSheet = .forward(message)
                            },
                            detailsAction: {
                                activeSheet = .details(message)
                            },
                            retryAction: {
                                retry(message)
                            },
                            deleteAction: {
                                messagePendingDeletion = message
                            },
                            timestampRevealState: $timestampRevealState
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
                markReadAction(selectedID)
                replyTarget = nil
                messagePendingDeletion = nil
                timestampRevealState.end()
                scrollToBottom(using: proxy, animated: true)
            }
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            if dynamicTypeSize.isAccessibilitySize {
                ConversationHeader(
                    conversation: conversation,
                    linePresentation: linePresentation,
                    alternatives: conversations,
                    lines: lines,
                    usesAccessibilityLayout: true,
                    selectedID: animatedSelectedID
                )
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 6)
                .background(Color(uiColor: .systemBackground))
                .overlay(alignment: .bottom) {
                    Divider()
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 0) {
                if currentLine == nil {
                    Label("This SIM is no longer available.", systemImage: "simcard")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                        .background(.bar)
                }
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.circle.fill")
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                        .background(.bar)
                }
                MessageComposer(
                    draft: $draft,
                    isTextEntryEnabled: currentLine?.isOnline == true && !isSending,
                    canSend: currentLine?.isOnline == true
                        && !isSending
                        && !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                    replyPreview: replyTarget.map(messagePreview),
                    attachmentAction: {
                        errorMessage = String(localized: "Image attachments are not available yet.")
                    },
                    cancelReplyAction: {
                        withAnimation(motionAnimation) {
                            replyTarget = nil
                        }
                    },
                    sendAction: send
                )
            }
        }
        .navigationTitle(dynamicTypeSize.isAccessibilitySize ? "" : conversation.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                if !dynamicTypeSize.isAccessibilitySize {
                    ConversationHeader(
                        conversation: conversation,
                        linePresentation: linePresentation,
                        alternatives: conversations,
                        lines: lines,
                        usesAccessibilityLayout: false,
                        selectedID: animatedSelectedID
                    )
                }
            }
        }
        .onAppear {
            markReadAction(selectedID)
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case let .attachment(attachment):
                AttachmentPreview(attachment: attachment)
            case let .forward(message):
                NewMessageScreen(
                    lines: lines,
                    selectedLineID: currentLine?.id,
                    message: message.body,
                    sendAction: sendAction
                )
            case let .details(message):
                MessageDetailsScreen(
                    message: message,
                    linePresentation: linePresentation
                )
            }
        }
        .alert(
            "Delete Message?",
            isPresented: deletionConfirmationIsPresented
        ) {
            Button("Delete", role: .destructive) {
                guard let messagePendingDeletion else { return }
                let removesConversation = conversation.messages.count == 1
                do {
                    try deleteAction(messagePendingDeletion.id)
                    withAnimation(motionAnimation) {
                        if replyTarget?.id == messagePendingDeletion.id {
                            replyTarget = nil
                        }
                    }
                    if removesConversation {
                        dismiss()
                    }
                } catch {
                    errorMessage = error.localizedDescription
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

    private var currentLine: DeviceLineIdentity? {
        lines.first(where: { $0.id == conversation.lineID })
    }

    private var linePresentation: LineHistoryPresentation {
        conversation.linePresentation(in: lines)
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

    private func send() {
        guard let currentLine,
              currentLine.isOnline,
              !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !isSending else { return }
        let body = draft
        let lineID = currentLine.id
        let remoteNumber = conversation.remoteNumber
        errorMessage = nil
        isSending = true
        Task {
            do {
                try await sendAction(lineID, remoteNumber, body)
                withAnimation(motionAnimation) {
                    draft = ""
                    replyTarget = nil
                }
            } catch {
                errorMessage = error.localizedDescription
            }
            isSending = false
        }
    }

    private func retry(_ message: MessageItem) {
        guard message.status == .failed else { return }
        guard let currentLine else {
            errorMessage = String(localized: "This SIM is no longer available.")
            return
        }
        guard currentLine.isOnline else {
            errorMessage = String(localized: "This SIM is offline.")
            return
        }
        errorMessage = nil
        Task {
            do {
                try await retryAction(message.id)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func scrollToBottom(using proxy: ScrollViewProxy, animated: Bool = false) {
        guard let lastID = conversation.messages.last?.id else { return }

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

#Preview("Conversation - Removed SIM") {
    NavigationStack {
        ConversationScreen(
            initialID: PreviewFixtures.conversations[0].id,
            conversations: [PreviewFixtures.conversations[0]],
            lines: []
        )
    }
}
#endif
