// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class DialerContactMatcherTests: XCTestCase {
    private let contacts = [
        ContactRecord(
            id: "chen",
            givenName: "坚方",
            familyName: "陈",
            organizationName: "",
            phoneNumbers: [
                ContactPhoneNumber(id: "chen-mobile", label: "mobile", value: "+86 138 0000 0000")
            ],
            thumbnailImageData: nil
        )
    ]

    func testPartialNumberFindsContactWithoutInventingPhoneLabel() {
        let matches = DialerContactMatcher.matches(number: "2025", contacts: contacts)

        XCTAssertEqual(matches.map(\.contact.displayName), ["陈坚方"])
        XCTAssertEqual(matches.first?.phoneNumber.label, "mobile")
    }

    func testExactMatchIgnoresFormatting() {
        let match = DialerContactMatcher.exactMatch(
            number: "+86 138 0000 0000",
            contacts: contacts
        )

        XCTAssertEqual(match?.contact.displayName, "陈坚方")
    }

    func testRepeatedDeleteThroughEmptyStringIsSafe() {
        var number = "1"
        for _ in 0..<20 {
            number = DialerNumberMutation.deletingLastCharacter(from: number)
        }

        XCTAssertEqual(number, "")
    }
}
