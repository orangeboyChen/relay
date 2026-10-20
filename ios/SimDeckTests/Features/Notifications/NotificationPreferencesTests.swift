// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class NotificationPreferencesTests: XCTestCase {
    func testDefaultsEnableMessageAndCallNotificationsButHidePreview() {
        let defaults = UserDefaults(suiteName: "NotificationPreferencesTests.defaults")!
        defaults.removePersistentDomain(forName: "NotificationPreferencesTests.defaults")

        let preferences = NotificationPreferences(defaults: defaults)

        XCTAssertTrue(preferences.messageNotifications)
        XCTAssertTrue(preferences.incomingCallNotifications)
        XCTAssertFalse(preferences.showMessagePreview)
    }

    func testPreferencesRoundTripThroughUserDefaults() {
        let defaults = UserDefaults(suiteName: "NotificationPreferencesTests.roundtrip")!
        defaults.removePersistentDomain(forName: "NotificationPreferencesTests.roundtrip")
        defaults.set(false, forKey: NotificationPreferences.messageNotificationsKey)
        defaults.set(true, forKey: NotificationPreferences.incomingCallNotificationsKey)
        defaults.set(true, forKey: NotificationPreferences.showMessagePreviewKey)

        let actual = NotificationPreferences(defaults: defaults)
        XCTAssertFalse(actual.messageNotifications)
        XCTAssertTrue(actual.incomingCallNotifications)
        XCTAssertTrue(actual.showMessagePreview)
    }

    func testMigratesExistingPreferencesIntoSharedDefaults() {
        let standard = UserDefaults(suiteName: "NotificationPreferencesTests.standard")!
        let shared = UserDefaults(suiteName: "NotificationPreferencesTests.shared")!
        standard.removePersistentDomain(forName: "NotificationPreferencesTests.standard")
        shared.removePersistentDomain(forName: "NotificationPreferencesTests.shared")
        standard.set(false, forKey: NotificationPreferences.messageNotificationsKey)
        standard.set(true, forKey: NotificationPreferences.showMessagePreviewKey)

        NotificationPreferences.migrateToSharedDefaults(standard: standard, shared: shared)

        XCTAssertFalse(shared.bool(forKey: NotificationPreferences.messageNotificationsKey))
        XCTAssertTrue(shared.bool(forKey: NotificationPreferences.showMessagePreviewKey))
    }
}
