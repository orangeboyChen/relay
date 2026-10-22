// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var historyEditMode: EditMode = .inactive
    @State private var contacts: [ContactRecord] = []
    @State private var selectedDetail: CallDetailSelection?

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
            onDelete: model.deleteCallHistoryRecords(withIDs:),
            openDetail: { record, contact in
                selectedDetail = CallDetailSelection(
                    record: record,
                    linePresentation: record.linePresentation(in: model.devices),
                    contact: contact
                )
            }
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
        .scrollEdgeEffectStyle(.soft, for: .all)
        .navigationDestination(isPresented: detailIsPresented) {
            if let selectedDetail {
                if let contact = selectedDetail.contact {
                    ContactDetailScreen(
                        contact: contact,
                        lines: model.devices,
                        defaultMessageLineID: selectedDetail.record.lineID,
                        callAction: { number in
                            await model.handleExternalDial(
                                number: number,
                                requiredLineID: selectedDetail.record.lineID
                            )
                        },
                        sendAction: { lineID, number, body in
                            try await model.sendMessage(
                                lineID: lineID,
                                remoteNumber: number,
                                body: body
                            )
                        }
                    )
                    .toolbar(.hidden, for: .tabBar)
                } else {
                    CallDetailScreen(
                        record: selectedDetail.record,
                        linePresentation: selectedDetail.linePresentation,
                        lines: model.devices,
                        defaultMessageLineID: model.defaultMessageLineID,
                        callAction: {
                            Task {
                                await model.handleExternalDial(
                                    number: selectedDetail.record.remoteNumber,
                                    requiredLineID: selectedDetail.record.lineID
                                )
                            }
                        },
                        sendAction: { lineID, number, body in
                            try await model.sendMessage(
                                lineID: lineID,
                                remoteNumber: number,
                                body: body
                            )
                        }
                    )
                    .toolbar(.hidden, for: .tabBar)
                }
            }
        }
    }

    private var detailIsPresented: Binding<Bool> {
        Binding(
            get: { selectedDetail != nil },
            set: { isPresented in
                if !isPresented {
                    selectedDetail = nil
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
