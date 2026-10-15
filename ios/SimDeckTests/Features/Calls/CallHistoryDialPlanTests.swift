import XCTest
@testable import SimDeck

final class CallHistoryDialPlanTests: XCTestCase {
    func testPlanUsesTheCallRecordsOriginalLineAndNormalizesItsNumber() throws {
        let selectedLine = makeLine(id: UUID(), isOnline: true)
        let otherLine = makeLine(id: UUID(), isOnline: true)
        let record = makeRecord(lineID: selectedLine.id, remoteNumber: "+86 135 0000 0000")

        let plan = try CallHistoryDialPlan(record: record, lines: [otherLine, selectedLine])

        XCTAssertEqual(plan.lineID, selectedLine.id)
        XCTAssertEqual(plan.number, "+8613500000000")
    }

    func testPlanRejectsAnOfflineOriginalLine() {
        let line = makeLine(id: UUID(), isOnline: false)
        let record = makeRecord(lineID: line.id, remoteNumber: "10086")

        XCTAssertThrowsError(try CallHistoryDialPlan(record: record, lines: [line])) { error in
            XCTAssertEqual(error as? CallHistoryDialPlanError, .lineOffline)
        }
    }

    func testPlanRejectsAMissingLine() {
        let record = makeRecord(lineID: UUID(), remoteNumber: "10086")

        XCTAssertThrowsError(try CallHistoryDialPlan(record: record, lines: [])) { error in
            XCTAssertEqual(error as? CallHistoryDialPlanError, .lineUnavailable)
        }
    }

    func testPlanRejectsAnInvalidRecordedNumber() {
        let line = makeLine(id: UUID(), isOnline: true)
        let record = makeRecord(lineID: line.id, remoteNumber: "not a number")

        XCTAssertThrowsError(try CallHistoryDialPlan(record: record, lines: [line])) { error in
            XCTAssertEqual(error as? CallHistoryDialPlanError, .invalidNumber)
        }
    }

    private func makeLine(id: UUID, isOnline: Bool) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: id,
            numericID: 1,
            deviceName: "Test modem",
            lineName: "Test line",
            phoneNumber: "13500000000",
            isOnline: isOnline
        )
    }

    private func makeRecord(lineID: UUID, remoteNumber: String) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: lineID,
            remoteNumber: remoteNumber,
            contactName: nil,
            direction: .outgoing,
            result: .answered,
            startedAt: Date(timeIntervalSince1970: 100),
            endedAt: Date(timeIntervalSince1970: 120)
        )
    }
}
