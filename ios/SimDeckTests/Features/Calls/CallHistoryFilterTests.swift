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

    private func record(result: CallResult) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "10086",
            contactName: nil,
            direction: .incoming,
            result: result,
            startedAt: .now,
            endedAt: .now
        )
    }
}
