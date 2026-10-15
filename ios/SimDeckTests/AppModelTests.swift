import XCTest
@testable import SimDeck

final class AppModelTests: XCTestCase {
    func testLineDisplayNamePrefersPhoneNumber() {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Office EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true
        )

        XCTAssertEqual(line.displayName, "13500000000")
    }

    func testLineDisplayNameFallsBackToNumericID() {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Office EC20",
            lineName: "",
            phoneNumber: "",
            isOnline: true
        )

        XCTAssertEqual(line.displayName, "SIM 2")
    }

    func testCallDurationUsesRecordedDates() {
        let start = Date(timeIntervalSince1970: 1_000)
        let call = CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "10086",
            contactName: "Carrier",
            direction: .incoming,
            result: .answered,
            startedAt: start,
            endedAt: start.addingTimeInterval(119)
        )

        XCTAssertEqual(call.displayName, "Carrier")
        XCTAssertEqual(call.duration, 119)
    }
}
