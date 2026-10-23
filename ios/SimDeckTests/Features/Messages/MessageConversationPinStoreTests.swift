// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageConversationPinStoreTests: XCTestCase {
    func testPinStateIsStoredPerConversation() throws {
        let suiteName = "MessageConversationPinStoreTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let first = ConversationID(deviceUUID: UUID(), remoteNumber: "10086")
        let second = ConversationID(deviceUUID: first.deviceUUID, remoteNumber: "10010")

        XCTAssertFalse(MessageConversationPinStore.isPinned(first, defaults: defaults))
        MessageConversationPinStore.setPinned(true, conversationID: first, defaults: defaults)
        XCTAssertTrue(MessageConversationPinStore.isPinned(first, defaults: defaults))
        XCTAssertFalse(MessageConversationPinStore.isPinned(second, defaults: defaults))

        MessageConversationPinStore.setPinned(false, conversationID: first, defaults: defaults)
        XCTAssertFalse(MessageConversationPinStore.isPinned(first, defaults: defaults))
    }
}
