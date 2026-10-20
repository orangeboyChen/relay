// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

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

    func testMissingLineKeepsHistoricalSIMPresentationButCannotBeCalled() {
        let line = makeLine(id: UUID(), isOnline: true)
        var record = makeRecord(lineID: line.id, remoteNumber: "10086")
        record.lineSnapshot = LineHistorySnapshot(line: line)

        let presentation = record.linePresentation(in: [])

        XCTAssertEqual(presentation.snapshot?.numericID, 1)
        XCTAssertEqual(presentation.displayName, "13500000000")
        XCTAssertFalse(presentation.isAvailable)
        XCTAssertThrowsError(try CallHistoryDialPlan(record: record, lines: [])) { error in
            XCTAssertEqual(error as? CallHistoryDialPlanError, .lineUnavailable)
        }
    }

    func testCurrentLinePresentationOverridesTheHistoricalSnapshot() {
        var currentLine = makeLine(id: UUID(), isOnline: true)
        currentLine.numericID = 2
        currentLine.phoneNumber = "13800000000"
        let staleSnapshot = LineHistorySnapshot(
            numericID: 1,
            lineName: "Old line",
            phoneNumber: "13500000000"
        )

        let presentation = LineHistoryPresentation(
            line: currentLine,
            snapshot: staleSnapshot
        )

        XCTAssertEqual(presentation.snapshot?.numericID, 2)
        XCTAssertEqual(presentation.displayName, "13800000000")
        XCTAssertTrue(presentation.isAvailable)
        XCTAssertTrue(presentation.isOnline)
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
