import XCTest
@testable import SimDeck

final class DeviceManagementTests: XCTestCase {
    func testDeviceSettingsDraftPreservesDeviceIdentity() {
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
        XCTAssertTrue(draft.isValid)

        draft.deviceName = "Office Modem"
        XCTAssertTrue(draft.isValid)
        XCTAssertEqual(draft.deviceName, "Office Modem")
    }
}
