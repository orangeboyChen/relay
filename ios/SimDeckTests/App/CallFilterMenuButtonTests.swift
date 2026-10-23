// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit
import XCTest
@testable import SimDeck

@MainActor
final class CallFilterMenuButtonTests: XCTestCase {
    func testSelectedSIMAppearsInNativeMenuSubtitleAndCheckmark() throws {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 1,
            deviceName: "Modem",
            lineName: "Primary",
            phoneNumber: "13500000000",
            isOnline: true
        )
        let coordinator = CallFilterMenuButton.Coordinator(
            filter: .constant(.all),
            selectedLineID: .constant(line.id)
        )

        let rootMenu = coordinator.makeMenu(lines: [line])
        let simMenu = try XCTUnwrap(rootMenu.children.last as? UIMenu)
        let selectedAction = try XCTUnwrap(simMenu.children.last as? UIAction)

        XCTAssertEqual(simMenu.title, "SIM")
        XCTAssertEqual(simMenu.subtitle, "SIM 1 · 13500000000")
        XCTAssertEqual(selectedAction.title, "13500000000")
        XCTAssertEqual(selectedAction.subtitle, "SIM 1")
        XCTAssertEqual(selectedAction.state, .on)
    }

    func testAllSIMsStartsCheckedWithoutASelectedLine() throws {
        let coordinator = CallFilterMenuButton.Coordinator(
            filter: .constant(.all),
            selectedLineID: .constant(nil)
        )

        let rootMenu = coordinator.makeMenu(lines: [])
        let simMenu = try XCTUnwrap(rootMenu.children.last as? UIMenu)
        let allSIMsAction = try XCTUnwrap(simMenu.children.first as? UIAction)

        XCTAssertEqual(simMenu.title, "SIM")
        XCTAssertEqual(simMenu.subtitle, "All SIMs")
        XCTAssertEqual(allSIMsAction.title, "All SIMs")
        XCTAssertEqual(allSIMsAction.state, .on)
    }
}
