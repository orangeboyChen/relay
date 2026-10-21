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

    func testEntriesGroupAdjacentMatchingCallsFromTheSameDay() {
        let calendar = Calendar(identifier: .gregorian)
        let lineID = UUID()
        let day = Date(timeIntervalSince1970: 1_800_000_000)
        let records = [
            record(
                lineID: lineID,
                number: "+86 135 0000 0000",
                direction: .outgoing,
                result: .answered,
                startedAt: day.addingTimeInterval(120)
            ),
            record(
                lineID: lineID,
                number: "+8613500000000",
                direction: .outgoing,
                result: .answered,
                startedAt: day
            ),
            record(
                lineID: lineID,
                number: "+8613500000000",
                direction: .incoming,
                result: .answered,
                startedAt: day.addingTimeInterval(-60)
            ),
        ]

        let entries = CallHistoryEntry.grouped(records, calendar: calendar)

        XCTAssertEqual(entries.map(\.records.count), [2, 1])
        XCTAssertEqual(entries[0].recordIDs, Set(records.prefix(2).map(\.id)))
    }

    func testEntriesDoNotGroupMatchingCallsAcrossDays() {
        let calendar = Calendar(identifier: .gregorian)
        let lineID = UUID()
        let firstDay = Date(timeIntervalSince1970: 1_800_000_000)
        let records = [
            record(lineID: lineID, startedAt: firstDay),
            record(lineID: lineID, startedAt: firstDay.addingTimeInterval(-86_400)),
        ]

        XCTAssertEqual(
            CallHistoryEntry.grouped(records, calendar: calendar).map(\.records.count),
            [1, 1]
        )
    }

    func testDetailSelectionCarriesMatchedContact() {
        let phoneNumber = ContactPhoneNumber(
            id: "mobile",
            label: "mobile",
            value: "135 0000 0000"
        )
        let contact = ContactRecord(
            id: "contact",
            givenName: "Test",
            familyName: "Contact",
            organizationName: "",
            phoneNumbers: [phoneNumber],
            thumbnailImageData: nil
        )
        let call = record(number: "+86 135 0000 0000")
        let linePresentation = LineHistoryPresentation(
            line: nil,
            snapshot: LineHistorySnapshot(
                numericID: 1,
                lineName: "Primary",
                phoneNumber: "13500000000"
            )
        )

        let selection = CallDetailSelection(
            record: call,
            linePresentation: linePresentation,
            contact: ContactRecordIndex(contacts: [contact]).contact(for: call.remoteNumber)
        )

        XCTAssertEqual(selection.contact, contact)
    }

    private func record(
        lineID: UUID = UUID(),
        number: String = "10086",
        direction: CallDirection = .incoming,
        result: CallResult = .answered,
        startedAt: Date = .now
    ) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: lineID,
            remoteNumber: number,
            contactName: nil,
            direction: direction,
            result: result,
            startedAt: startedAt,
            endedAt: startedAt
        )
    }
}
