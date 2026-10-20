// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class DialPadInputTests: XCTestCase {
    func testZeroTapProducesZero() {
        XCTAssertEqual(DialPadInput.output(for: "0", press: .tap), "0")
    }

    func testZeroLongPressProducesOnlyPlus() {
        XCTAssertEqual(DialPadInput.output(for: "0", press: .longPress), "+")
    }

    func testOtherKeysHaveNoLongPressAlternateInput() {
        XCTAssertNil(DialPadInput.output(for: "1", press: .longPress))
        XCTAssertNil(DialPadInput.output(for: "#", press: .longPress))
    }
}
