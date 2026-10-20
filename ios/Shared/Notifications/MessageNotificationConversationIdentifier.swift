// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

enum MessageNotificationConversationIdentifier {
    static func make(
        simID: UUID?,
        slotID: UUID?,
        deviceUUID: UUID,
        remoteNumber: String
    ) -> String {
        let scope: String
        if let simID {
            scope = "sim:\(simID.uuidString.lowercased())"
        } else if let slotID {
            scope = "slot:\(slotID.uuidString.lowercased())"
        } else {
            scope = "device:\(deviceUUID.uuidString.lowercased())"
        }

        let normalizedNumber = remoteNumber.filter { character in
            !character.isWhitespace && !"-()".contains(character)
        }
        let digest = SHA256.hash(data: Data(normalizedNumber.utf8))
            .map { String(format: "%02x", $0) }
            .joined()
        return "\(scope):\(digest)"
    }
}
