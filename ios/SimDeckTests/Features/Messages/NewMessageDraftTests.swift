// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class NewMessageDraftTests: XCTestCase {
    func testDraftRequiresOnlineSelectedLineValidRecipientAndBody() {
        let onlineLine = makeLine(isOnline: true)
        var draft = NewMessageDraft(
            selectedLineID: onlineLine.id,
            recipient: "13800138000",
            body: "Hello"
        )

        XCTAssertTrue(draft.canSend(using: [onlineLine]))

        draft.body = "  \n"
        XCTAssertFalse(draft.canSend(using: [onlineLine]))

        draft.body = "Hello"
        draft.recipient = "1"
        XCTAssertFalse(draft.canSend(using: [onlineLine]))
    }

    func testDraftRejectsOfflineOrMissingLine() {
        let offlineLine = makeLine(isOnline: false)
        let draft = NewMessageDraft(
            selectedLineID: offlineLine.id,
            recipient: "10086",
            body: "Hello"
        )

        XCTAssertFalse(draft.canSend(using: [offlineLine]))
        XCTAssertFalse(draft.canSend(using: []))
    }

    private func makeLine(isOnline: Bool) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: UUID(),
            numericID: 1,
            deviceName: "Test modem",
            lineName: "Test line",
            phoneNumber: "13500000000",
            isOnline: isOnline
        )
    }
}
