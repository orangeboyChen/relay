// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageTimestampFormatterTests: XCTestCase {
    func testUsesClockTimeForMessagesFromToday() {
        let calendar = Calendar.current
        let referenceDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 21, hour: 22, minute: 45))!
        let messageDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 21, hour: 22, minute: 40))!

        XCTAssertEqual(
            MessageTimestampFormatter.string(from: messageDate, relativeTo: referenceDate),
            messageDate.formatted(.dateTime.hour().minute())
        )
    }

    func testUsesYesterdayForMessagesFromThePreviousDay() {
        let calendar = Calendar.current
        let referenceDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 21, hour: 9))!
        let messageDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 20, hour: 22))!

        let formatter = RelativeDateTimeFormatter()
        formatter.dateTimeStyle = .named
        formatter.unitsStyle = .full
        XCTAssertEqual(
            MessageTimestampFormatter.string(from: messageDate, relativeTo: referenceDate),
            formatter.localizedString(for: messageDate, relativeTo: referenceDate).capitalized
        )
    }

    func testYesterdayIsRelativeToTheInjectedReferenceDate() {
        let calendar = Calendar.current
        let referenceDate = calendar.date(from: DateComponents(year: 2035, month: 6, day: 15, hour: 9))!
        let messageDate = calendar.date(from: DateComponents(year: 2035, month: 6, day: 14, hour: 22))!

        let formatter = RelativeDateTimeFormatter()
        formatter.dateTimeStyle = .named
        formatter.unitsStyle = .full

        XCTAssertEqual(
            MessageTimestampFormatter.string(from: messageDate, relativeTo: referenceDate),
            formatter.localizedString(for: messageDate, relativeTo: referenceDate).capitalized
        )
    }

    func testUsesWeekdayForEarlierMessagesThisWeek() {
        let calendar = Calendar.current
        let referenceDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 21, hour: 9))!
        let messageDate = calendar.date(from: DateComponents(year: 2026, month: 10, day: 19, hour: 9))!

        XCTAssertEqual(
            MessageTimestampFormatter.string(from: messageDate, relativeTo: referenceDate),
            messageDate.formatted(.dateTime.weekday(.wide))
        )
    }
}
