// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageNotificationConversationIdentifierTests: XCTestCase {
    private let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
    private let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000002")!
    private let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000003")!

    func testUsesSIMIdentityBeforeSlotAndDeviceIdentity() {
        let identifier = make(simID: simID, slotID: slotID, number: "+86 135-0000-0000")

        XCTAssertTrue(identifier.hasPrefix("sim:\(simID.uuidString.lowercased()):"))
        XCTAssertFalse(identifier.contains("+8613500000000"))
    }

    func testGroupsEquivalentNumberFormattingOnTheSameSIM() {
        XCTAssertEqual(
            make(simID: simID, slotID: slotID, number: "+86 135 0000 0000"),
            make(simID: simID, slotID: slotID, number: "+8613500000000")
        )
    }

    func testKeepsDifferentSIMsInSeparateGroups() {
        let otherSIM = UUID(uuidString: "01900000-0000-7000-8000-000000000004")!

        XCTAssertNotEqual(
            make(simID: simID, slotID: slotID, number: "13500000000"),
            make(simID: otherSIM, slotID: slotID, number: "13500000000")
        )
    }

    func testFallsBackToSlotThenDeviceForLegacyPayloads() {
        XCTAssertTrue(make(simID: nil, slotID: slotID, number: "10086").hasPrefix("slot:"))
        XCTAssertTrue(make(simID: nil, slotID: nil, number: "10086").hasPrefix("device:"))
    }

    private func make(simID: UUID?, slotID: UUID?, number: String) -> String {
        MessageNotificationConversationIdentifier.make(
            simID: simID,
            slotID: slotID,
            deviceUUID: deviceID,
            remoteNumber: number
        )
    }
}
