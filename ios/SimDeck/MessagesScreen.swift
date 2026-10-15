import SwiftUI

struct MessagesScreen: View {
    let state: MessageListState
    let lines: [DeviceLineIdentity]

    @State private var searchText = ""
    @State private var selectedLineID: UUID?
    @State private var isComposing = false

    var body: some View {
        Group {
            switch state {
            case .loading:
                ProgressView("Loading messages")
            case .loaded(let conversations) where conversations.isEmpty:
                ContentUnavailableView(
                    "No Messages",
                    systemImage: "message",
                    description: Text("Messages sent through sim-deck appear here.")
                )
            case .loaded(let conversations):
                conversationList(conversations)
            case .failed:
                ContentUnavailableView(
                    "Messages Unavailable",
                    systemImage: "exclamationmark.triangle",
                    description: Text("Check the server connection and try again.")
                )
            }
        }
        .navigationTitle("Messages")
        .searchable(text: $searchText, prompt: "Search messages")
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                SIMFilterMenu(lines: lines, selection: $selectedLineID)
                Button {
                    isComposing = true
                } label: {
                    Label("New Message", systemImage: "square.and.pencil")
                }
            }
        }
        .sheet(isPresented: $isComposing) {
            NewMessageScreen(lines: lines)
        }
    }

    private func conversationList(_ conversations: [MessageConversation]) -> some View {
        let filtered = conversations.filter { conversation in
            (selectedLineID == nil || conversation.lineID == selectedLineID)
                && conversation.matches(searchText)
        }

        return Group {
            if filtered.isEmpty {
                ContentUnavailableView.search(text: searchText)
            } else {
                List(filtered) { conversation in
                    if let line = lines.first(where: { $0.id == conversation.lineID }) {
                        NavigationLink {
                            ConversationScreen(
                                initialID: conversation.id,
                                conversations: conversations.filter {
                                    $0.remoteNumber == conversation.remoteNumber
                                },
                                lines: lines
                            )
                        } label: {
                            ConversationRow(conversation: conversation, line: line)
                        }
                    }
                }
                .listStyle(.plain)
            }
        }
    }
}

private struct SIMFilterMenu: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    var body: some View {
        Menu {
            Button("All SIMs") {
                selection = nil
            }
            ForEach(lines) { line in
                Button("SIM \(line.numericID) · \(line.displayName)") {
                    selection = line.id
                }
            }
        } label: {
            Label("Filter by SIM", systemImage: selection == nil ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
        }
    }
}

private struct ConversationRow: View {
    let conversation: MessageConversation
    let line: DeviceLineIdentity

    var body: some View {
        HStack(spacing: 12) {
            SIMBadge(line: line)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(conversation.displayName)
                        .fontWeight(conversation.unreadCount > 0 ? .semibold : .regular)
                        .lineLimit(1)
                    Spacer()
                    if let lastMessage = conversation.lastMessage {
                        Text(lastMessage.sentAt, format: .dateTime.hour().minute())
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                HStack {
                    Text(summary)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                    Spacer(minLength: 8)
                    if conversation.unreadCount > 0 {
                        Text(conversation.unreadCount, format: .number)
                            .font(.caption2.bold())
                            .foregroundStyle(.white)
                            .frame(minWidth: 20, minHeight: 20)
                            .background(.blue, in: Circle())
                            .accessibilityLabel("\(conversation.unreadCount) unread messages")
                    }
                }
            }
        }
        .padding(.vertical, 5)
    }

    private var summary: String {
        guard let lastMessage = conversation.lastMessage else {
            return String(localized: "No messages yet")
        }
        if lastMessage.body.isEmpty, lastMessage.attachment != nil {
            return String(localized: "Image")
        }
        return lastMessage.body
    }
}

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
            MessageComposer(draft: $draft)
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

private struct MessageComposer: View {
    @Binding var draft: String

    var body: some View {
        HStack(alignment: .bottom, spacing: 10) {
            Button { } label: {
                Image(systemName: "photo")
                    .frame(width: 44, height: 44)
            }
            .accessibilityLabel("Add image")

            TextField("Message", text: $draft, axis: .vertical)
                .lineLimit(1...5)
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .background(.quaternary, in: Capsule())

            Button { } label: {
                Image(systemName: "arrow.up.circle.fill")
                    .font(.title)
                    .frame(width: 44, height: 44)
            }
            .accessibilityLabel("Send message")
            .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.bar)
    }
}

private struct NewMessageScreen: View {
    let lines: [DeviceLineIdentity]

    @Environment(\.dismiss) private var dismiss
    @State private var selectedLineID: UUID?
    @State private var recipient = ""
    @State private var message = ""

    var body: some View {
        NavigationStack {
            Form {
                Picker("SIM", selection: $selectedLineID) {
                    Text("Choose a SIM").tag(UUID?.none)
                    ForEach(lines.filter(\.isOnline)) { line in
                        Text("SIM \(line.numericID) · \(line.displayName)").tag(Optional(line.id))
                    }
                }
                TextField("Recipient", text: $recipient)
                    .keyboardType(.phonePad)
                TextField("Message", text: $message, axis: .vertical)
                    .lineLimit(3...8)
            }
            .navigationTitle("New Message")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Send") { dismiss() }
                        .disabled(selectedLineID == nil || recipient.isEmpty || message.isEmpty)
                }
            }
        }
    }
}

#if DEBUG
#Preview("Messages") {
    NavigationStack {
        MessagesScreen(state: .loaded(PreviewFixtures.conversations), lines: PreviewFixtures.devices)
    }
}

#Preview("Conversation") {
    NavigationStack {
        ConversationScreen(
            initialID: PreviewFixtures.conversations[0].id,
            conversations: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices
        )
    }
}

#Preview("Messages - Empty") {
    NavigationStack {
        MessagesScreen(state: .loaded([]), lines: PreviewFixtures.devices)
    }
}

#Preview("Messages - Loading") {
    NavigationStack {
        MessagesScreen(state: .loading, lines: PreviewFixtures.devices)
    }
}

#Preview("Messages - Offline") {
    NavigationStack {
        MessagesScreen(state: .failed, lines: PreviewFixtures.devices)
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Messages - Simplified Chinese") {
    NavigationStack {
        MessagesScreen(state: .loaded(PreviewFixtures.conversations), lines: PreviewFixtures.devices)
    }
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}
#endif
