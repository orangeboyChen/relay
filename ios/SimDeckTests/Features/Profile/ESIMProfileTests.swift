// This Source Code Form is subject to the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class ESIMProfileTests: XCTestCase {
    func testDisplayNamePrefersCustomNameThenProfileIDThenICCID() {
        XCTAssertEqual(
            ESIMProfile(profileID: "profile-1", name: "Travel").displayName,
            "Travel"
        )
        XCTAssertEqual(
            ESIMProfile(profileID: "profile-1").displayName,
            "profile-1"
        )
        XCTAssertEqual(
            ESIMProfile(profileID: "", iccid: "8986000000000000001").displayName,
            "8986000000000000001"
        )
    }

    func testDetailSubtitleContainsOperatorAndICCID() {
        let profile = ESIMProfile(
            profileID: "profile-1",
            iccid: "8986000000000000001",
            operatorName: "Example Mobile"
        )

        XCTAssertEqual(
            profile.detailSubtitle,
            "Example Mobile · 8986000000000000001"
        )
    }
}
