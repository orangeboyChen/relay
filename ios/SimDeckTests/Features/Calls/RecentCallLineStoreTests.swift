// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class RecentCallLineStoreTests: XCTestCase {
    func testExactCallIdentifierWinsOverTheLatestRouteForTheSameNumber() throws {
        let suiteName = "RecentCallLineStoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        var currentDate = Date(timeIntervalSince1970: 1_000)
        let store = UserDefaultsRecentCallLineStore(defaults: defaults, now: { currentDate })
        let earlierCallID = UUID()
        let earlierLineID = UUID()
        let latestLineID = UUID()

        store.record(callID: earlierCallID, remoteNumber: "+86 135 0000 0000", lineID: earlierLineID)
        currentDate.addTimeInterval(60)
        store.record(callID: UUID(), remoteNumber: "+8613500000000", lineID: latestLineID)

        XCTAssertEqual(
            store.lineID(
                callRecordIdentifier: earlierCallID.uuidString,
                remoteNumber: "+8613500000000"
            ),
            earlierLineID
        )
        XCTAssertEqual(
            store.lineID(callRecordIdentifier: nil, remoteNumber: "+8613500000000"),
            latestLineID
        )
    }

    func testRoutesSurviveStoreRecreationWithoutPersistingPlaintextNumber() throws {
        let suiteName = "RecentCallLineStoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        let callID = UUID()
        let lineID = UUID()
        let store = UserDefaultsRecentCallLineStore(defaults: defaults)

        store.record(callID: callID, remoteNumber: "13500000000", lineID: lineID)

        let reloadedStore = UserDefaultsRecentCallLineStore(defaults: defaults)
        XCTAssertEqual(
            reloadedStore.lineID(
                callRecordIdentifier: callID.uuidString,
                remoteNumber: "13500000000"
            ),
            lineID
        )
        let persistedValues = defaults.dictionaryRepresentation().values
        let persistedText = persistedValues.compactMap { value -> String? in
            guard let data = value as? Data else { return nil }
            return String(data: data, encoding: .utf8)
        }.joined()
        XCTAssertFalse(persistedText.contains("13500000000"))
    }

    func testRoutesExpireAfterCallHistoryRetentionPeriod() throws {
        let suiteName = "RecentCallLineStoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        var currentDate = Date(timeIntervalSince1970: 1_000)
        let store = UserDefaultsRecentCallLineStore(defaults: defaults, now: { currentDate })

        store.record(callID: UUID(), remoteNumber: "10086", lineID: UUID())
        currentDate.addTimeInterval(366 * 24 * 60 * 60)

        XCTAssertNil(store.lineID(callRecordIdentifier: nil, remoteNumber: "10086"))
    }
}
