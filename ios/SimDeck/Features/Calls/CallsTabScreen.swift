// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var historyEditMode: EditMode = .inactive

    var body: some View {
        CallHistoryScreen(
            state: model.callHistory,
            lines: model.devices,
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
    }
}
