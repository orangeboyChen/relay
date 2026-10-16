import XCTest
@testable import SimDeck

final class DeviceManagementTests: XCTestCase {
    func testSettingsDraftPreservesEditableProfileAndValidatesSIMRange() {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Living Room EC20",
            lineName: "Personal",
            phoneNumber: "13500000000",
            isOnline: true
        )

        var draft = DeviceSettingsDraft(line: line)

        XCTAssertEqual(draft.deviceName, line.deviceName)
        XCTAssertEqual(draft.lineName, line.lineName)
        XCTAssertEqual(draft.phoneNumber, line.phoneNumber)
        XCTAssertTrue(draft.isValid)

        draft.numericID = 100
        XCTAssertFalse(draft.isValid)
    }

}
