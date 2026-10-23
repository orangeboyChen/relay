// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import UserNotifications

enum AlertNotificationMetadata {
    static let otpCategoryIdentifier = "simdeck.sms.otp"
    static let copyCodeActionIdentifier = "simdeck.sms.copy-code"
    static let otpUserInfoKey = "simdeck.sms.otp"

    static func conversationIdentifier(
        content: UNNotificationContent,
        actionIdentifier: String
    ) -> String? {
        guard actionIdentifier == UNNotificationDefaultActionIdentifier else { return nil }
        let targetIdentifier = content.targetContentIdentifier?.trimmingCharacters(
            in: .whitespacesAndNewlines
        )
        let identifier = if let targetIdentifier, !targetIdentifier.isEmpty {
            targetIdentifier
        } else {
            content.threadIdentifier
        }
        return identifier.isEmpty ? nil : identifier
    }
}
