// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class CallHistoryFilterTests: XCTestCase {
    func testMissedFilterExcludesOtherResults() {
        let records = [
            record(result: .answered),
            record(result: .missed),
            record(result: .rejected),
            record(result: .failed),
        ]

        XCTAssertEqual(CallHistoryFilter.missed.records(from: records).map(\.result), [.missed])
        XCTAssertEqual(CallHistoryFilter.all.records(from: records), records)
    }

    func testFilterCombinesCallResultAndSelectedSIM() {
        let firstLineID = UUID()
        let secondLineID = UUID()
        let records = [
            record(lineID: firstLineID, result: .missed),
            record(lineID: firstLineID, result: .answered),
            record(lineID: secondLineID, result: .missed),
        ]

        XCTAssertEqual(
            CallHistoryFilter.missed.records(from: records, lineID: firstLineID),
            [records[0]]
        )
        XCTAssertEqual(
            CallHistoryFilter.all.records(from: records, lineID: secondLineID),
            [records[2]]
        )
        XCTAssertEqual(
            CallHistoryFilter.all.records(from: records, lineID: nil),
            records
        )
    }

    private func record(lineID: UUID = UUID(), result: CallResult) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: lineID,
            remoteNumber: "10086",
            contactName: nil,
            direction: .incoming,
            result: result,
            startedAt: .now,
            endedAt: .now
        )
    }
}
