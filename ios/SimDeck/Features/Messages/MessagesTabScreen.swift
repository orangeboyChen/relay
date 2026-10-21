// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct MessagesTabScreen: View {
    @Bindable var model: AppModel

    @State private var contacts: [ContactRecord] = []

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
