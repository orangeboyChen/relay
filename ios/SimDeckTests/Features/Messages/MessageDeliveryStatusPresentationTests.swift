// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageDeliveryStatusPresentationTests: XCTestCase {
    func testDeliveryStatusIsShownOnlyForSentMessages() {
        let received = MessageItem(
            id: UUID(),
            direction: .received,
            body: "Incoming",
            attachment: nil,
            sentAt: .now,
            status: .delivered
        )
        let sent = MessageItem(
            id: UUID(),
            direction: .sent,
            body: "Outgoing",
            attachment: nil,
            sentAt: .now,
            status: .delivered
        )

        XCTAssertFalse(received.displaysDeliveryStatus)
        XCTAssertTrue(sent.displaysDeliveryStatus)
    }
}
