// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct NewMessageDraft: Equatable {
    var selectedLineID: UUID?
    var recipient: String
    var body: String

    var hasValidRecipient: Bool {
        let value = recipient.filter { !$0.isWhitespace && !"-()".contains($0) }
        guard (2...32).contains(value.count) else { return false }

        return value.enumerated().allSatisfy { index, character in
            character.isNumber || (character == "+" && index == 0)
        }
    }

    func canSend(using lines: [DeviceLineIdentity]) -> Bool {
        guard let selectedLineID,
              lines.contains(where: { $0.id == selectedLineID && $0.isOnline })
        else {
            return false
        }

        return hasValidRecipient
            && !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}
