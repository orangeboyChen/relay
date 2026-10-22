// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct ConversationScreen: View {
    let conversations: [MessageConversation]
    let lines: [DeviceLineIdentity]
    let contacts: [ContactRecord]
    let defaultMessageLineID: UUID?
    let sendAction: (UUID, String, String) async throws -> Void
    let sendImageAction: (UUID, String, String, MessageImageAttachment) async throws -> Void
    let retryAction: (UUID) async throws -> Void
    let deleteAction: (Set<UUID>) throws -> Void
    let markReadAction: (ConversationID) -> Void
    let callAction: (String) async -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Namespace private var contactTransitionNamespace

    @State private var selectedLineID: UUID?
    @State private var sendLineID: UUID?
    @State private var draft = ""
    @State private var draftAttachment: MessageImageAttachment?
    @State private var activeSheet: ConversationSheet?
    @State private var isContactDetailPresented = false
    @State private var messagePendingDeletion: MessageItem?
    @State private var timestampRevealModel = MessageTimestampRevealModel()
    @State private var isSending = false
    @State private var errorMessage: String?

    init(
        initialID: ConversationID,
        conversations: [MessageConversation],
        lines: [DeviceLineIdentity],
        contacts: [ContactRecord] = [],
        defaultMessageLineID: UUID? = nil,
        sendAction: @escaping (UUID, String, String) async throws -> Void = { _, _, _ in },
        sendImageAction: @escaping (
            UUID,
            String,
            String,
            MessageImageAttachment
        ) async throws -> Void = { _, _, _, _ in },
        retryAction: @escaping (UUID) async throws -> Void = { _ in },
        deleteAction: @escaping (Set<UUID>) throws -> Void = { _ in },
        markReadAction: @escaping (ConversationID) -> Void = { _ in },
        callAction: @escaping (String) async -> Void = { _ in }
    ) {
        self.conversations = conversations
        self.lines = lines
        self.contacts = contacts
        self.defaultMessageLineID = defaultMessageLineID
        self.sendAction = sendAction
        self.sendImageAction = sendImageAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
        self.markReadAction = markReadAction
        self.callAction = callAction
        _selectedLineID = State(initialValue: conversations.count > 1 ? nil : initialID.deviceUUID)
        _sendLineID = State(initialValue: initialID.deviceUUID)
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 3) {
                    ForEach(timelineEntries) { entry in
                        VStack(spacing: 3) {
                            if entry.showsTimestampSeparator {
                                ConversationTimestampSeparator(date: entry.message.sentAt)
                            }

                            MessageBubble(
                                message: entry.message,
                                lineSnapshot: entry.lineSnapshot,
                                showsLineBadge: selectedLineID == nil,
                                canRetry: sendLine(for: entry.lineID)?.isOnline == true,
                                openAttachment: {
                                    guard let attachment = entry.message.attachment else { return }
                                    activeSheet = .attachment(attachment)
                                },
                                copyAction: {
                                    UIPasteboard.general.string = entry.message.body
                                },
                                copyVerificationCodeAction: {
                                    guard let code = VerificationCodeDetector.code(
                                        in: entry.message.body
                                    ) else { return }
                                    UIPasteboard.general.string = code
                                },
                                forwardAction: {
                                    activeSheet = .forward(entry.message)
                                },
                                detailsAction: {
                                    activeSheet = .details(
                                        MessageDetailsSelection(
                                            message: entry.message,
                                            linePresentation: LineHistoryPresentation(
                                                line: sendLine(for: entry.lineID),
                                                snapshot: entry.lineSnapshot
                                            )
                                        )
                                    )
                                },
                                retryAction: {
                                    retry(entry.message)
                                },
                                deleteAction: {
                                    messagePendingDeletion = entry.message
                                },
                                timestampRevealModel: timestampRevealModel
                            )
                        }
                        .id(entry.id)
                    }

                    Color.clear
                        .frame(height: 1)
                        .id(ConversationScrollAnchor.bottom)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
                .id(selectedLineID)
                .transition(.opacity)
                .modifier(
                    ConversationTimestampGestureModifier(
                        model: timestampRevealModel,
                        reduceMotion: reduceMotion
                    )
                )
            }
            .contentShape(Rectangle())
            .scrollDismissesKeyboard(.interactively)
            .defaultScrollAnchor(.top, for: .alignment)
            .task(id: selectedLineID) {
                scrollToBottom(using: proxy)
            }
            .onChange(of: timelineEntries.count) {
                scrollToBottom(using: proxy)
            }
            .onChange(of: dynamicTypeSize) {
                scrollToBottom(using: proxy)
            }
            .onChange(of: selectedLineID) { _, lineID in
                if let lineID {
                    sendLineID = lineID
                }
                markVisibleConversationsRead()
                messagePendingDeletion = nil
                timestampRevealModel.end()
                scrollToBottom(using: proxy, animated: true)
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
                    attachment: $draftAttachment,
                    availableLines: availableSendLines,
                    selectedLineID: $sendLineID,
                    showsLinePicker: selectedLineID == nil,
                    isTextEntryEnabled: currentLine?.isOnline == true && !isSending,
                    canSend: currentLine?.isOnline == true
                        && !isSending
                        && (
                            draftAttachment != nil
                                || !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        ),
                    sendAction: send
                )
            }
        }
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .toolbar {
            ToolbarItem(placement: .principal) {
                ConversationHeader(
                    contact: displayedContact,
                    usesAccessibilityLayout: dynamicTypeSize.isAccessibilitySize,
                    transitionID: contactTransitionID,
                    transitionNamespace: contactTransitionNamespace,
                    action: {
                        isContactDetailPresented = true
                    }
                )
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
            }

            ToolbarItem(placement: .topBarTrailing) {
                lineSelectionControl
            }
        }
        .onAppear {
            markVisibleConversationsRead()
        }
        .navigationDestination(isPresented: $isContactDetailPresented) {
            ContactDetailScreen(
                contact: displayedContact,
                lines: lines,
                defaultMessageLineID: defaultMessageLineID ?? currentLine?.id,
                callAction: callAction,
                sendAction: sendAction
            )
            .navigationTransition(
                .zoom(
                    sourceID: contactTransitionID,
                    in: contactTransitionNamespace
                )
            )
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
            case let .details(selection):
                MessageDetailsScreen(
                    message: selection.message,
                    linePresentation: selection.linePresentation
                )
            }
        }
        .alert(
            "Delete Message?",
            isPresented: deletionConfirmationIsPresented
        ) {
            Button("Delete", role: .destructive) {
                guard let messagePendingDeletion else { return }
                let removesConversation = conversations.flatMap(\.messages).count == 1
                do {
                    try deleteAction([messagePendingDeletion.id])
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
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private var conversation: MessageConversation {
        if let selectedLineID,
           let selected = conversations.first(where: { $0.lineID == selectedLineID }) {
            return selected
        }
        return conversations.first
            ?? MessageConversation(
                lineID: sendLineID ?? UUID(),
                remoteNumber: "",
                contactName: nil,
                unreadCount: 0,
                messages: []
            )
    }

    private var visibleConversations: [MessageConversation] {
        guard let selectedLineID else { return conversations }
        return conversations.filter { $0.lineID == selectedLineID }
    }

    private var currentLine: DeviceLineIdentity? {
        guard let sendLineID else { return nil }
        return sendLine(for: sendLineID)
    }

    private var timelineEntries: [ConversationTimelineEntry] {
        let messages = visibleConversations
            .flatMap { conversation in
                conversation.messages.map { message in
                    ConversationTimelineEntry(
                        message: message,
                        lineID: conversation.lineID,
                        lineSnapshot: conversation.linePresentation(in: lines).snapshot,
                        showsTimestampSeparator: false
                    )
                }
            }
            .sorted { $0.message.sentAt < $1.message.sentAt }

        return messages.enumerated().map { index, entry in
            let previousMessage = index > 0 ? messages[index - 1].message : nil
            return ConversationTimelineEntry(
                message: entry.message,
                lineID: entry.lineID,
                lineSnapshot: entry.lineSnapshot,
                showsTimestampSeparator: shouldShowTimestamp(
                    before: entry.message,
                    after: previousMessage
                )
            )
        }
    }

    private var availableSendLines: [DeviceLineIdentity] {
        conversations.compactMap { conversation in
            lines.first(where: { $0.id == conversation.lineID && $0.isOnline })
        }
    }

    private var displayedContact: ContactRecord {
        if let contact = ContactRecordIndex(contacts: contacts).contact(
            for: conversation.remoteNumber
        ) {
            return contact
        }

        let name = conversation.contactName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return ContactRecord(
            id: "message-\(conversation.id.deviceUUID)-\(conversation.id.remoteNumber)",
            givenName: name,
            familyName: "",
            organizationName: "",
            phoneNumbers: [
                ContactPhoneNumber(
                    id: "message-\(conversation.id.deviceUUID)-\(conversation.id.remoteNumber)-phone",
                    label: String(localized: "mobile"),
                    value: conversation.remoteNumber
                ),
            ],
            thumbnailImageData: nil
        )
    }

    private var contactTransitionID: String {
        "message-contact-\(DialString.normalized(conversation.remoteNumber))"
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

    private var lineSelectionControl: some View {
        Menu {
            Picker("SIM", selection: $selectedLineID) {
                Label("All SIMs", systemImage: "simcard.2")
                    .tag(nil as UUID?)
                ForEach(conversations) { alternative in
                    let alternativeLine = alternative.linePresentation(in: lines)
                    SIMLabel(snapshot: alternativeLine.snapshot)
                        .tag(alternative.lineID as UUID?)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Image(
                systemName: selectedLineID == nil
                    ? "line.3.horizontal.decrease"
                    : "line.3.horizontal.decrease.circle.fill"
            )
                .font(.body.weight(.semibold))
                .frame(width: 44, height: 44)
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .glassEffect(.regular.interactive(), in: Circle())
        .accessibilityLabel("Filter by SIM")
        .accessibilityValue(lineFilterAccessibilityValue)
    }

    private var lineFilterAccessibilityValue: Text {
        guard let selectedLineID,
              let selectedConversation = conversations.first(where: {
                  $0.lineID == selectedLineID
              }) else {
            return Text("All SIMs")
        }
        let presentation = selectedConversation.linePresentation(in: lines)
        let label = SIMLabelPresentation(
            snapshot: presentation.snapshot,
            titleStyle: .displayName
        )
        return Text(label.accessibilityLabel)
    }

    private func markVisibleConversationsRead() {
        for conversation in visibleConversations {
            markReadAction(conversation.id)
        }
    }

    private func sendLine(for lineID: UUID) -> DeviceLineIdentity? {
        lines.first(where: { $0.id == lineID })
    }


    private var motionAnimation: Animation? {
        reduceMotion ? nil : .snappy
    }

    private func send() {
        guard let currentLine,
              currentLine.isOnline,
              draftAttachment != nil
                || !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !isSending else { return }
        let body = draft
        let attachment = draftAttachment
        let lineID = currentLine.id
        let remoteNumber = conversation.remoteNumber
        errorMessage = nil
        isSending = true
        Task {
            do {
                if let attachment {
                    try await sendImageAction(lineID, remoteNumber, body, attachment)
                } else {
                    try await sendAction(lineID, remoteNumber, body)
                }
                withAnimation(motionAnimation) {
                    draft = ""
                    draftAttachment = nil
                }
            } catch {
                errorMessage = error.localizedDescription
            }
            isSending = false
        }
    }

    private func shouldShowTimestamp(
        before message: MessageItem,
        after previousMessage: MessageItem?
    ) -> Bool {
        guard let previousMessage else { return true }
        if !Calendar.current.isDate(previousMessage.sentAt, inSameDayAs: message.sentAt) {
            return true
        }
        return message.sentAt.timeIntervalSince(previousMessage.sentAt) >= 15 * 60
    }

    private func retry(_ message: MessageItem) {
        guard message.status == .failed else { return }
        guard let conversation = conversations.first(where: { conversation in
            conversation.messages.contains(where: { $0.id == message.id })
        }), let retryLine = sendLine(for: conversation.lineID) else {
            errorMessage = String(localized: "This SIM is no longer available.")
            return
        }
        guard retryLine.isOnline else {
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
        guard !timelineEntries.isEmpty else { return }

        Task { @MainActor in
            await Task.yield()
            if animated {
                withAnimation(motionAnimation) {
                    proxy.scrollTo(ConversationScrollAnchor.bottom, anchor: .bottom)
                }
            } else {
                proxy.scrollTo(ConversationScrollAnchor.bottom, anchor: .bottom)
                await Task.yield()
                proxy.scrollTo(ConversationScrollAnchor.bottom, anchor: .bottom)
            }
        }
    }
}

private struct ConversationTimestampGestureModifier: ViewModifier {
    let model: MessageTimestampRevealModel
    let reduceMotion: Bool

    func body(content: Content) -> some View {
        content.background {
            MessageHorizontalSwipeGesture(
                onChanged: { horizontal, vertical in
                    model.update(horizontal: horizontal, vertical: vertical)
                },
                onEnded: { _, _ in
                    withAnimation(reduceMotion ? nil : .snappy) {
                        model.end()
                    }
                }
            )
        }
    }
}

private struct ConversationTimelineEntry: Identifiable {
    let message: MessageItem
    let lineID: UUID
    let lineSnapshot: LineHistorySnapshot?
    let showsTimestampSeparator: Bool

    var id: UUID { message.id }
}

private struct ConversationTimestampSeparator: View {
    let date: Date

    var body: some View {
        Text(timestamp)
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity)
            .padding(.top, 14)
            .padding(.bottom, 5)
            .accessibilityLabel(
                Text(date, format: .dateTime.year().month().day().hour().minute())
            )
    }

    private var timestamp: String {
        let calendar = Calendar.current
        if calendar.isDateInToday(date) {
            return String(
                localized: "Today \(date.formatted(date: .omitted, time: .shortened))"
            )
        }
        if calendar.isDateInYesterday(date) {
            return String(
                localized: "Yesterday \(date.formatted(date: .omitted, time: .shortened))"
            )
        }
        if calendar.component(.year, from: date) == calendar.component(.year, from: .now) {
            return date.formatted(
                .dateTime.month(.abbreviated).day().hour().minute()
            )
        }
        return date.formatted(
            .dateTime.year().month(.abbreviated).day().hour().minute()
        )
    }
}

private enum ConversationScrollAnchor: Hashable {
    case bottom
}

private struct MessageDetailsSelection: Identifiable {
    let message: MessageItem
    let linePresentation: LineHistoryPresentation

    var id: UUID { message.id }
}

private enum ConversationSheet: Identifiable {
    case attachment(MessageImageAttachment)
    case forward(MessageItem)
    case details(MessageDetailsSelection)

    var id: String {
        switch self {
        case let .attachment(attachment):
            "attachment-\(attachment.id)"
        case let .forward(message):
            "forward-\(message.id)"
        case let .details(selection):
            "details-\(selection.id)"
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
