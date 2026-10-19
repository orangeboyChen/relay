import SwiftUI

struct MessagesScreen: View {
    let state: MessageListState
    let lines: [DeviceLineIdentity]
    let defaultLineID: UUID?
    let refreshAction: () async -> Void
    let sendAction: (UUID, String, String) async throws -> Void
    let retryAction: (UUID) async throws -> Void
    let markReadAction: (ConversationID) -> Void

    @State private var searchText = ""
    @State private var selectedLineID: UUID?
    @State private var composition: MessageComposition?

    init(
        state: MessageListState,
        lines: [DeviceLineIdentity],
        defaultLineID: UUID? = nil,
        refreshAction: @escaping () async -> Void = {},
        sendAction: @escaping (UUID, String, String) async throws -> Void = { _, _, _ in },
        retryAction: @escaping (UUID) async throws -> Void = { _ in },
        markReadAction: @escaping (ConversationID) -> Void = { _ in }
    ) {
        self.state = state
        self.lines = lines
        self.defaultLineID = defaultLineID
        self.refreshAction = refreshAction
        self.sendAction = sendAction
        self.retryAction = retryAction
        self.markReadAction = markReadAction
    }

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
        .task {
            await refreshAction()
        }
        .refreshable {
            await refreshAction()
        }
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                SIMFilterMenu(lines: lines, selection: $selectedLineID)
                Button {
                    composition = MessageComposition()
                } label: {
                    Label("New Message", systemImage: "square.and.pencil")
                }
                .disabled(!hasOnlineLine)
                .accessibilityHint(hasOnlineLine ? Text("") : Text("An online SIM is required."))
            }
        }
        .sheet(item: $composition) { composition in
            NewMessageScreen(
                lines: lines,
                selectedLineID: defaultLineID,
                sendAction: sendAction
            )
            .id(composition.id)
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
                                    $0.lineID == conversation.lineID
                                        && $0.remoteNumber == conversation.remoteNumber
                                },
                                lines: lines,
                                sendAction: sendAction,
                                retryAction: retryAction,
                                markReadAction: markReadAction
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

    private var hasOnlineLine: Bool {
        lines.contains(where: \.isOnline)
    }
}

private struct MessageComposition: Identifiable {
    let id = UUID()
}

#if DEBUG
#Preview("Messages") {
    NavigationStack {
        MessagesScreen(state: .loaded(PreviewFixtures.conversations), lines: PreviewFixtures.devices)
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

#Preview("Messages - Dark Accessibility") {
    NavigationStack {
        MessagesScreen(state: .loaded(PreviewFixtures.conversations), lines: PreviewFixtures.devices)
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
