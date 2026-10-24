// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class CallNumberDisplayTests: XCTestCase {
    func testEmptyDTMFInputKeepsTheOriginalNumber() {
        XCTAssertEqual(
            CallNumberDisplayValue.text(originalNumber: "10086", enteredDigits: ""),
            "10086"
        )
    }

    func testDTMFInputReplacesTheOriginalNumber() {
        XCTAssertEqual(
            CallNumberDisplayValue.text(originalNumber: "10086", enteredDigits: "12"),
            "12"
        )
    }
}
