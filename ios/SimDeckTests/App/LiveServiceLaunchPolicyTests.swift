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
