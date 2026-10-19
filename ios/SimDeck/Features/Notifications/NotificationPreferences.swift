import Foundation

struct NotificationPreferences: Equatable, Sendable {
    static let appGroupIdentifier = "group.com.nowcent.simdeck"
    static let messageNotificationsKey = "sim-deck.notifications.messages"
    static let incomingCallNotificationsKey = "sim-deck.notifications.calls"
    static let showMessagePreviewKey = "sim-deck.notifications.message-preview"

    var messageNotifications: Bool
    var incomingCallNotifications: Bool
    var showMessagePreview: Bool

    static var sharedDefaults: UserDefaults {
        UserDefaults(suiteName: appGroupIdentifier) ?? .standard
    }

    static func migrateToSharedDefaults(
        standard: UserDefaults = .standard,
        shared: UserDefaults = sharedDefaults
    ) {
        for key in [messageNotificationsKey, incomingCallNotificationsKey, showMessagePreviewKey]
        where shared.object(forKey: key) == nil {
            if let value = standard.object(forKey: key) {
                shared.set(value, forKey: key)
            }
        }
    }

    init(defaults: UserDefaults = sharedDefaults) {
        messageNotifications = defaults.object(forKey: Self.messageNotificationsKey) as? Bool ?? true
        incomingCallNotifications = defaults.object(forKey: Self.incomingCallNotificationsKey) as? Bool ?? true
        showMessagePreview = defaults.object(forKey: Self.showMessagePreviewKey) as? Bool ?? false
    }

    func save(to defaults: UserDefaults = sharedDefaults) {
        defaults.set(messageNotifications, forKey: Self.messageNotificationsKey)
        defaults.set(incomingCallNotifications, forKey: Self.incomingCallNotificationsKey)
        defaults.set(showMessagePreview, forKey: Self.showMessagePreviewKey)
    }
}
