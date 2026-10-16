import XCTest
@testable import SimDeck

final class DeviceManagementTests: XCTestCase {
    func testPairingDetectorFindsOnlyNewDeviceIdentity() {
        let existing = makeLine(id: UUID(), numericID: 1)
        let added = makeLine(id: UUID(), numericID: 2)

        let detected = DevicePairingDetector.newDevice(
            previousIDs: [existing.id],
            devices: [existing, added]
        )

        XCTAssertEqual(detected, added)
        XCTAssertNil(
            DevicePairingDetector.newDevice(
                previousIDs: [existing.id, added.id],
                devices: [existing, added]
            )
        )
    }

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

    private func makeLine(id: UUID, numericID: Int) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: id,
            numericID: numericID,
            deviceName: "Modem \(numericID)",
            lineName: "Line \(numericID)",
            phoneNumber: "",
            isOnline: true
        )
    }
}
