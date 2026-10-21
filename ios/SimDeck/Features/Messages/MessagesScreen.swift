// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct MessagesScreen: View {
    let state: MessageListState
    let lines: [DeviceLineIdentity]
    let referenceDate: Date
    let defaultLineID: UUID?
    let refreshAction: () async -> Void
    let sendAction: (UUID, String, String) async throws -> Void
    let retryAction: (UUID) async throws -> Void
    let deleteAction: (Set<UUID>) throws -> Void
    let markReadAction: (ConversationID) -> Void
    let markUnreadAction: (ConversationID) -> Void
    let isMutedAction: (ConversationID) -> Bool
    let setMutedAction: (ConversationID, Bool) -> Void

    @State private var searchText = ""
    @State private var selectedLineID: UUID?
    @State private var composition: MessageComposition?
    @State private var editMode: EditMode = .inactive
    @State private var selectedConversationIDs: Set<ConversationID> = []
    @State private var conversationsPendingDeletion: [MessageConversation] = []
    @State private var deleteErrorMessage: String?
    @State private var selectedConversationID: ConversationID?
    @State private var mutedConversationIDs: Set<ConversationID> = []

    init(
        state: MessageListState,
        lines: [DeviceLineIdentity],
        referenceDate: Date = .now,
        defaultLineID: UUID? = nil,
        refreshAction: @escaping () async -> Void = {},
        sendAction: @escaping (UUID, String, String) async throws -> Void = { _, _, _ in },
        retryAction: @escaping (UUID) async throws -> Void = { _ in },
        deleteAction: @escaping (Set<UUID>) throws -> Void = { _ in },
        markReadAction: @escaping (ConversationID) -> Void = { _ in },
        markUnreadAction: @escaping (ConversationID) -> Void = { _ in },
        isMutedAction: @escaping (ConversationID) -> Bool = { _ in false },
        setMutedAction: @escaping (ConversationID, Bool) -> Void = { _, _ in }
    ) {
        self.state = state
        self.lines = lines
        self.referenceDate = referenceDate
        self.defaultLineID = defaultLineID
        self.refreshAction = refreshAction
        self.sendAction = sendAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
        self.markReadAction = markReadAction
        self.markUnreadAction = markUnreadAction
        self.isMutedAction = isMutedAction
        self.setMutedAction = setMutedAction
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
        .navigationBarTitleDisplayMode(.inline)
        .environment(\.editMode, $editMode)
        .task {
            await refreshAction()
            synchronizeMutedConversations()
        }
        .refreshable {
            await refreshAction()
        }
        .toolbar {
            if case .loaded(let conversations) = state, !conversations.isEmpty {
                ToolbarItem(placement: .topBarLeading) {
                    Button(editMode.isEditing ? "Done" : "Edit") {
                        withAnimation(.snappy) {
                            editMode = editMode.isEditing ? .inactive : .active
                        }
                    }
                    .controlSize(.large)
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                SIMFilterMenu(lines: lines, selection: $selectedLineID)
                    .controlSize(.large)
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if editMode.isEditing {
                messageSelectionBar
            } else if case .loaded(let conversations) = state, !conversations.isEmpty {
                MessageListBottomBar(
                    searchText: $searchText,
                    canCompose: hasOnlineLine,
                    composeAction: {
                        composition = MessageComposition()
                    }
                )
            }
        }
        .onChange(of: editMode) { _, newMode in
            if !newMode.isEditing {
                selectedConversationIDs.removeAll()
            }
        }
        .alert("Delete Conversations?", isPresented: deletionConfirmationIsPresented) {
            Button("Delete", role: .destructive) {
                deletePendingConversations()
            }
            Button("Cancel", role: .cancel) {
                conversationsPendingDeletion = []
            }
        } message: {
            Text("All messages in the selected conversations will be removed from this iPhone.")
        }
        .alert("Unable to Delete", isPresented: deleteErrorIsPresented) {
            Button("OK", role: .cancel) {
                deleteErrorMessage = nil
            }
        } message: {
            Text(deleteErrorMessage ?? "")
        }
        .sheet(item: $composition) { composition in
            NewMessageScreen(
                lines: lines,
                selectedLineID: defaultLineID,
                sendAction: sendAction
            )
            .id(composition.id)
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
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
                List(selection: $selectedConversationIDs) {
                    ForEach(filtered) { conversation in
                        conversationRow(conversation)
                            .tag(conversation.id)
                            .listRowInsets(
                                EdgeInsets(top: 0, leading: 26, bottom: 0, trailing: 16)
                            )
                            .alignmentGuide(.listRowSeparatorLeading) { dimensions in
                                dimensions[.leading] + 58
                            }
                    }
                }
                .listStyle(.plain)
                .onChange(of: filtered.map(\.id)) { _, conversationIDs in
                    selectedConversationIDs.formIntersection(conversationIDs)
                }
                .navigationDestination(isPresented: selectedConversationIsPresented) {
                    if let selectedConversationID,
                       let conversation = conversations.first(where: {
                           $0.id == selectedConversationID
                       }) {
                        ConversationScreen(
                            initialID: conversation.id,
                            conversations: conversation.lineAlternatives(in: conversations),
                            lines: lines,
                            backUnreadCount: conversations.reduce(into: 0) { count, item in
                                count += item.unreadCount
                            },
                            sendAction: sendAction,
                            retryAction: retryAction,
                            deleteAction: deleteAction,
                            markReadAction: markReadAction
                        )
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func conversationRow(_ conversation: MessageConversation) -> some View {
        if editMode.isEditing {
            ConversationRow(
                conversation: conversation,
                linePresentation: conversation.linePresentation(in: lines),
                referenceDate: referenceDate,
                isMuted: mutedConversationIDs.contains(conversation.id),
                isEditing: true
            )
            .contentShape(.rect)
        } else {
            Button {
                selectedConversationID = conversation.id
            } label: {
                ConversationRow(
                    conversation: conversation,
                    linePresentation: conversation.linePresentation(in: lines),
                referenceDate: referenceDate,
                    isMuted: mutedConversationIDs.contains(conversation.id),
                    isEditing: false
                )
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .swipeActions(edge: .leading, allowsFullSwipe: true) {
                if conversation.unreadCount > 0 {
                    Button {
                        markReadAction(conversation.id)
                    } label: {
                        Image(systemName: "message.badge.filled.fill")
                    }
                    .tint(.blue)
                    .accessibilityLabel("Read")
                } else {
                    Button {
                        markUnreadAction(conversation.id)
                    } label: {
                        Image(systemName: "message.badge.filled.fill")
                    }
                    .tint(.blue)
                    .accessibilityLabel("Unread")
                }
            }
            .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                Button(role: .destructive) {
                    conversationsPendingDeletion = [conversation]
                } label: {
                    Image(systemName: "trash")
                }
                .accessibilityLabel("Delete")

                let isMuted = mutedConversationIDs.contains(conversation.id)
                Button {
                    setConversationMuted(!isMuted, id: conversation.id)
                } label: {
                    Image(systemName: isMuted ? "bell.fill" : "bell.slash.fill")
                }
                .tint(.indigo)
                .accessibilityLabel(isMuted ? "Show Alerts" : "Hide Alerts")
            }
        }
    }

    private var messageSelectionBar: some View {
        HStack {
            Text("\(selectedConversationIDs.count) Selected")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Spacer()
            Button(role: .destructive) {
                guard case .loaded(let conversations) = state else { return }
                conversationsPendingDeletion = conversations.filter {
                    selectedConversationIDs.contains($0.id)
                }
            } label: {
                Label("Delete", systemImage: "trash")
                    .fontWeight(.semibold)
                    .foregroundStyle(
                        selectedConversationIDs.isEmpty
                            ? AnyShapeStyle(.secondary)
                            : AnyShapeStyle(.red)
                    )
                    .frame(minHeight: 44)
                    .padding(.horizontal, 12)
                    .contentShape(Capsule())
            }
            .buttonStyle(.glass)
            .buttonBorderShape(.capsule)
            .disabled(selectedConversationIDs.isEmpty)
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 6)
        .background(.bar)
    }

    private var selectedConversationIsPresented: Binding<Bool> {
        Binding(
            get: { selectedConversationID != nil },
            set: { isPresented in
                if !isPresented {
                    selectedConversationID = nil
                }
            }
        )
    }

    private func synchronizeMutedConversations() {
        guard case .loaded(let conversations) = state else {
            mutedConversationIDs = []
            return
        }
        mutedConversationIDs = Set(
            conversations.lazy.filter { isMutedAction($0.id) }.map(\.id)
        )
    }

    private func setConversationMuted(_ isMuted: Bool, id: ConversationID) {
        setMutedAction(id, isMuted)
        if isMuted {
            mutedConversationIDs.insert(id)
        } else {
            mutedConversationIDs.remove(id)
        }
    }

    private var deletionConfirmationIsPresented: Binding<Bool> {
        Binding(
            get: { !conversationsPendingDeletion.isEmpty },
            set: { isPresented in
                if !isPresented { conversationsPendingDeletion = [] }
            }
        )
    }

    private var deleteErrorIsPresented: Binding<Bool> {
        Binding(
            get: { deleteErrorMessage != nil },
            set: { isPresented in
                if !isPresented { deleteErrorMessage = nil }
            }
        )
    }

    private func deletePendingConversations() {
        let messageIDs = Set(conversationsPendingDeletion.flatMap(\.messages).map(\.id))
        do {
            try deleteAction(messageIDs)
            selectedConversationIDs.subtract(conversationsPendingDeletion.map(\.id))
            conversationsPendingDeletion = []
            if selectedConversationIDs.isEmpty {
                editMode = .inactive
            }
        } catch {
            conversationsPendingDeletion = []
            deleteErrorMessage = error.localizedDescription
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

#Preview("Messages - Removed SIM") {
    NavigationStack {
        MessagesScreen(
            state: .loaded([PreviewFixtures.conversations[0]]),
            lines: []
        )
    }
}
#endif
