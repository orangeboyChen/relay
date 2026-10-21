// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageNotificationMuteStoreTests: XCTestCase {
    func testMuteStatePersistsAndCanBeRemoved() throws {
        let suiteName = "MessageNotificationMuteStoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        let identifier = "slot:example:remote"

        XCTAssertFalse(
            MessageNotificationMuteStore.isMuted(
                conversationIdentifier: identifier,
                defaults: defaults
            )
        )

        MessageNotificationMuteStore.setMuted(
            true,
            conversationIdentifier: identifier,
            defaults: defaults
        )

        XCTAssertTrue(
            MessageNotificationMuteStore.isMuted(
                conversationIdentifier: identifier,
                defaults: defaults
            )
        )

        MessageNotificationMuteStore.setMuted(
            false,
            conversationIdentifier: identifier,
            defaults: defaults
        )

        XCTAssertFalse(
            MessageNotificationMuteStore.isMuted(
                conversationIdentifier: identifier,
                defaults: defaults
            )
        )
    }
}
