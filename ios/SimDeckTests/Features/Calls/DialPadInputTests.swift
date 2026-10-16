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
