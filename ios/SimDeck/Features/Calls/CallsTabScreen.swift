// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var historyEditMode: EditMode = .inactive
    @State private var contacts: [ContactRecord] = []

    var body: some View {
        CallHistoryScreen(
            state: model.callHistory,
            lines: model.devices,
            contacts: contacts,
            referenceDate: .now,
            callStarter: model.callCoordinator,
            defaultMessageLineID: model.defaultMessageLineID,
            sendMessage: { lineID, number, body in
                try await model.sendMessage(lineID: lineID, remoteNumber: number, body: body)
            },
            editMode: $historyEditMode,
            onDelete: model.deleteCallHistoryRecords(withIDs:)
        ) { lineID in
            model.lastSuccessfulDialLineID = lineID
        }
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
