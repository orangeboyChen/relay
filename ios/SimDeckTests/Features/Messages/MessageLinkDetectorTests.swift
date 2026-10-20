// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageLinkDetectorTests: XCTestCase {
    func testDetectsWebLinksAndPhoneNumbers() {
        let result = MessageLinkDetector.attributedString(
            from: "Visit https://example.com or call +86 135 0000 0000"
        )
        let links = result.runs.compactMap(\.link)

        XCTAssertEqual(links.count, 2)
        XCTAssertEqual(links.first?.absoluteString, "https://example.com")
        XCTAssertEqual(links.last?.absoluteString, "tel:+8613500000000")
    }

    func testPlainMessageDoesNotCreateLink() {
        let result = MessageLinkDetector.attributedString(from: "hello world")

        XCTAssertTrue(result.runs.compactMap(\.link).isEmpty)
    }
}
