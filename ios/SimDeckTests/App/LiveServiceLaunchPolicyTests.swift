// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class LiveServiceLaunchPolicyTests: XCTestCase {
    func testDisablesLiveServicesForUnitTestHost() {
        XCTAssertFalse(LiveServiceLaunchPolicy.shouldStart(
            arguments: ["SimDeck"],
            environment: ["XCTestConfigurationFilePath": "/tmp/tests.xctestconfiguration"]
        ))
    }

    func testDisablesLiveServicesForPreviewFixtures() {
        XCTAssertFalse(LiveServiceLaunchPolicy.shouldStart(
            arguments: ["SimDeck", "--preview-fixtures"],
            environment: [:]
        ))
    }

    func testEnablesLiveServicesForNormalDebugLaunch() {
        XCTAssertTrue(LiveServiceLaunchPolicy.shouldStart(
            arguments: ["SimDeck"],
            environment: [:]
        ))
    }
}
