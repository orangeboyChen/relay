// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct MessagesTabScreen: View {
    @Bindable var model: AppModel

    @State private var contacts: [ContactRecord] = []
    @State private var selectedConversationID: ConversationID?

    var body: some View {
        MessagesScreen(
            state: model.messages,
            lines: model.devices,
            contacts: contacts,
            defaultLineID: model.defaultMessageLineID,
            refreshAction: {
                await model.refreshMessages()
            },
            sendAction: { lineID, remoteNumber, body in
                try await model.sendMessage(
                    lineID: lineID,
                    remoteNumber: remoteNumber,
                    body: body
                )
            },
            sendImageAction: { lineID, remoteNumber, body, attachment in
                try await model.sendImageMessage(
                    lineID: lineID,
                    remoteNumber: remoteNumber,
                    body: body,
                    attachment: attachment
                )
            },
            retryAction: { messageID in
                try await model.retryMessage(id: messageID)
            },
            deleteAction: { messageIDs in
                try model.deleteMessages(ids: messageIDs)
            },
            markReadAction: { conversationID in
                model.markConversationRead(conversationID)
            },
            markUnreadAction: { conversationID in
                model.markConversationUnread(conversationID)
            },
            isMutedAction: { conversationID in
                model.isConversationMuted(conversationID)
            },
            setMutedAction: { conversationID, isMuted in
                model.setConversationMuted(isMuted, id: conversationID)
            },
            openConversation: { conversationID in
                selectedConversationID = conversationID
            },
            callAction: { number in
                await model.handleExternalDial(number: number)
            }
        )
        .task {
            refreshContacts()
            for await _ in model.contactStore.changes() {
                guard !Task.isCancelled else { return }
                refreshContacts()
            }
        }
        .navigationDestination(isPresented: conversationIsPresented) {
            if let selectedConversationID,
               case .loaded(let conversations) = model.messages,
               let conversation = conversations.first(where: {
                   $0.id == selectedConversationID
               }) {
                ConversationScreen(
                    initialID: conversation.id,
                    conversations: conversation.lineAlternatives(in: conversations),
                    lines: model.devices,
                    contacts: contacts,
                    defaultMessageLineID: model.defaultMessageLineID,
                    backUnreadCount: conversations.reduce(into: 0) { count, item in
                        count += item.unreadCount
                    },
                    sendAction: { lineID, number, body in
                        try await model.sendMessage(
                            lineID: lineID,
                            remoteNumber: number,
                            body: body
                        )
                    },
                    sendImageAction: { lineID, number, body, attachment in
                        try await model.sendImageMessage(
                            lineID: lineID,
                            remoteNumber: number,
                            body: body,
                            attachment: attachment
                        )
                    },
                    retryAction: { messageID in
                        try await model.retryMessage(id: messageID)
                    },
                    deleteAction: { messageIDs in
                        try model.deleteMessages(ids: messageIDs)
                    },
                    markReadAction: { conversationID in
                        model.markConversationRead(conversationID)
                    },
                    callAction: { number in
                        await model.handleExternalDial(number: number)
                    }
                )
                .toolbar(.hidden, for: .tabBar)
            }
        }
    }

    private var conversationIsPresented: Binding<Bool> {
        Binding(
            get: { selectedConversationID != nil },
            set: { isPresented in
                if !isPresented {
                    selectedConversationID = nil
                }
            }
        )
    }

    private func refreshContacts() {
        switch model.contactStore.authorizationState {
        case .limited, .authorized:
            contacts = (try? model.contactStore.fetchContacts()) ?? []
        case .notDetermined, .denied:
            contacts = []
        }
    }
}
