// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum MessageNotificationMuteStore {
    static let appGroupIdentifier = "group.com.nowcent.simdeck"
    static let mutedConversationIdentifiersKey = "sim-deck.notifications.muted-conversations.v1"

    static var sharedDefaults: UserDefaults {
        UserDefaults(suiteName: appGroupIdentifier) ?? .standard
    }

    static func isMuted(
        conversationIdentifier: String,
        defaults: UserDefaults = sharedDefaults
    ) -> Bool {
        mutedConversationIdentifiers(defaults: defaults).contains(conversationIdentifier)
    }

    static func setMuted(
        _ isMuted: Bool,
        conversationIdentifier: String,
        defaults: UserDefaults = sharedDefaults
    ) {
        var identifiers = mutedConversationIdentifiers(defaults: defaults)
        if isMuted {
            identifiers.insert(conversationIdentifier)
        } else {
            identifiers.remove(conversationIdentifier)
        }
        defaults.set(identifiers.sorted(), forKey: mutedConversationIdentifiersKey)
    }

    private static func mutedConversationIdentifiers(defaults: UserDefaults) -> Set<String> {
        Set(defaults.stringArray(forKey: mutedConversationIdentifiersKey) ?? [])
    }
}
