import XCTest
@testable import SimDeck

final class CallDurationFormatterTests: XCTestCase {
    func testFormatsCallDurationsAsPositionalTimers() {
        XCTAssertEqual(CallDurationFormatter.string(elapsed: -1), "00:00")
        XCTAssertEqual(CallDurationFormatter.string(elapsed: 0), "00:00")
        XCTAssertEqual(CallDurationFormatter.string(elapsed: 9.9), "00:09")
        XCTAssertEqual(CallDurationFormatter.string(elapsed: 65), "01:05")
        XCTAssertEqual(CallDurationFormatter.string(elapsed: 3_661), "01:01:01")
    }
}
