// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct KeypadTabScreen: View {
    @Bindable var model: AppModel
    @State private var contacts: [ContactRecord] = []

    var body: some View {
        DialerScreen(
            lines: model.devices,
            contacts: contacts,
            initialLineID: preferredManualLineID,
            externalRequest: model.pendingExternalDialRequest,
            externalLineID: externalLineID,
            callStarter: model.callCoordinator,
            onStarted: { lineID in
                model.lastSuccessfulDialLineID = lineID
            },
            onExternalRequestConsumed: { requestID in
                guard model.pendingExternalDialRequest?.id == requestID else { return }
                model.pendingExternalDialRequest = nil
            }
        )
        .task {
            reloadContacts()
        }
    }

    private func reloadContacts() {
        switch model.contactStore.authorizationState {
        case .authorized, .limited:
            contacts = (try? model.contactStore.fetchContacts()) ?? []
        case .notDetermined, .denied:
            contacts = []
        }
    }

    private var preferredManualLineID: UUID? {
        if let defaultID = model.defaultCallLineID,
           model.devices.contains(where: { $0.id == defaultID && $0.isOnline }) {
            return defaultID
        }
        if let lastID = model.lastSuccessfulDialLineID,
           model.devices.contains(where: { $0.id == lastID && $0.isOnline }) {
            return lastID
        }
        return model.devices.first(where: \.isOnline)?.id
    }

    private var externalLineID: UUID? {
        guard let request = model.pendingExternalDialRequest else { return nil }
        switch request.lineSelection {
        case .automatic:
            guard case .ready(let line) = OutgoingCallLineRouter.resolve(
                lines: model.devices,
                preferredLineID: model.defaultCallLineID
            ) else { return nil }
            return line.id
        case .preferred(let lineID):
            return model.devices.contains(where: { $0.id == lineID && $0.isOnline })
                ? lineID
                : nil
        }
    }
}

#if DEBUG
#Preview("Keypad Tab") {
    NavigationStack {
        KeypadTabScreen(model: .preview(selectedTab: .keypad))
    }
}
#endif
