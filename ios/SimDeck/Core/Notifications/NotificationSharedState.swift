import Foundation

struct CachedDeviceKeys: Codable, Sendable {
    let verificationKey: Data
    let commandKey: Data
}

enum NotificationSharedState {
    static let deviceKeysKey = "sim-deck.notification.device-keys.v1"

    static func cacheDeviceKeys(_ keys: [UUID: CachedDeviceKeys], defaults: UserDefaults = NotificationPreferences.sharedDefaults) {
        let encoded = keys.reduce(into: [String: CachedDeviceKeys]()) { result, entry in
            result[entry.key.uuidString.lowercased()] = entry.value
        }
        guard let data = try? JSONEncoder().encode(encoded) else { return }
        defaults.set(data, forKey: deviceKeysKey)
    }

    static func deviceKeys(
        for deviceID: UUID,
        defaults: UserDefaults = NotificationPreferences.sharedDefaults
    ) -> CachedDeviceKeys? {
        guard let data = defaults.data(forKey: deviceKeysKey),
              let keys = try? JSONDecoder().decode([String: CachedDeviceKeys].self, from: data),
              let deviceKeys = keys[deviceID.uuidString.lowercased()],
              deviceKeys.verificationKey.count == 32,
              deviceKeys.commandKey.count == 32 else {
            return nil
        }
        return deviceKeys
    }

    static func clearDeviceKeys(defaults: UserDefaults = NotificationPreferences.sharedDefaults) {
        defaults.removeObject(forKey: deviceKeysKey)
    }
}
