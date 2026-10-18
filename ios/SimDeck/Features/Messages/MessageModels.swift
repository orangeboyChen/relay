import Foundation

struct ConversationID: Hashable, Sendable {
    let deviceUUID: UUID
    let remoteNumber: String
}

struct MessageConversation: Identifiable, Equatable, Sendable, Codable {
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

struct MessageItem: Identifiable, Equatable, Sendable, Codable {
    let id: UUID
    let sourceID: String?
    let direction: MessageDirection
    let body: String
    let attachment: MessageImageAttachment?
    let sentAt: Date
    var status: MessageDeliveryStatus

    init(
        id: UUID,
        sourceID: String? = nil,
        direction: MessageDirection,
        body: String,
        attachment: MessageImageAttachment?,
        sentAt: Date,
        status: MessageDeliveryStatus
    ) {
        self.id = id
        self.sourceID = sourceID
        self.direction = direction
        self.body = body
        self.attachment = attachment
        self.sentAt = sentAt
        self.status = status
    }
}

struct MessageImageAttachment: Identifiable, Equatable, Sendable, Codable {
    let id: UUID
    let systemImageName: String
}

enum MessageDirection: Equatable, Sendable, Codable {
    case received
    case sent
}

enum MessageDeliveryStatus: Equatable, Sendable, Codable {
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
