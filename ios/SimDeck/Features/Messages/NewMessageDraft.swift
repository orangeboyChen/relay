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
