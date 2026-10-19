import Foundation
import Security

struct NotificationOTPRecord: Codable, Equatable, Sendable {
    let code: String
    let receivedAt: Date
    let remoteNumber: String
    let numericID: Int
}

enum NotificationOTPStore {
    private static let service = "com.orangeboychen.simdeck.otp"
    private static let account = "recent-v1"
    private static let accessGroup = "97YACL9S6W.com.nowcent.simdeck.shared"
    private static let maximumAge: TimeInterval = 180

    static func load(now: Date = .now) -> [NotificationOTPRecord] {
        let data = readKeychain() ?? Data()
        let records = (try? JSONDecoder().decode([NotificationOTPRecord].self, from: data)) ?? []
        return records.filter { now.timeIntervalSince($0.receivedAt) <= maximumAge }
    }

    private static func readKeychain() -> Data? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess else { return nil }
        return result as? Data
    }
}
