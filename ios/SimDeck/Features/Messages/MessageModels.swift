import Foundation

struct ConversationID: Hashable, Sendable {
    let deviceUUID: UUID
    let remoteNumber: String
}

struct MessageConversation: Identifiable, Equatable, Sendable {
    let lineID: UUID
    let remoteNumber: String
    let contactName: String?
    var unreadCount: Int
    var messages: [MessageItem]

    var id: ConversationID {
        ConversationID(deviceUUID: lineID, remoteNumber: remoteNumber)
    }

    var displayName: String {
        guard let contactName, !contactName.isEmpty else { return remoteNumber }
        return contactName
    }

    var lastMessage: MessageItem? {
        messages.last
    }

    func matches(_ query: String) -> Bool {
        let normalizedQuery = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalizedQuery.isEmpty else { return true }

        return displayName.localizedCaseInsensitiveContains(normalizedQuery)
            || remoteNumber.localizedCaseInsensitiveContains(normalizedQuery)
            || messages.contains { $0.body.localizedCaseInsensitiveContains(normalizedQuery) }
    }
}

struct MessageItem: Identifiable, Equatable, Sendable {
    let id: UUID
    let direction: MessageDirection
    let body: String
    let attachment: MessageImageAttachment?
    let sentAt: Date
    let status: MessageDeliveryStatus
}

struct MessageImageAttachment: Identifiable, Equatable, Sendable {
    let id: UUID
    let systemImageName: String
}

enum MessageDirection: Equatable, Sendable {
    case received
    case sent
}

enum MessageDeliveryStatus: Equatable, Sendable {
    case sending
    case sent
    case delivered
    case failed
}

enum MessageListState: Equatable, Sendable {
    case loading
    case loaded([MessageConversation])
    case failed
}
