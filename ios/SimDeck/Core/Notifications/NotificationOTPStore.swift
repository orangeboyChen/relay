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
    private static let maximumRecords = 5

    static func append(code: String, remoteNumber: String, numericID: Int, now: Date = .now) {
        var records = load(now: now)
        records.removeAll { $0.code == code && $0.remoteNumber == remoteNumber }
        records.insert(
            NotificationOTPRecord(code: code, receivedAt: now, remoteNumber: remoteNumber, numericID: numericID),
            at: 0
        )
        save(Array(records.prefix(maximumRecords)))
    }

    static func clear() {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        _ = SecItemDelete(query as CFDictionary)
    }

    private static func load(now: Date) -> [NotificationOTPRecord] {
        let records = (try? JSONDecoder().decode([NotificationOTPRecord].self, from: readKeychain() ?? Data())) ?? []
        return records.filter { now.timeIntervalSince($0.receivedAt) <= maximumAge }
    }

    private static func save(_ records: [NotificationOTPRecord]) {
        guard let data = try? JSONEncoder().encode(records) else { return }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        let status = SecItemUpdate(query as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var insert = query
            insert[kSecValueData as String] = data
            insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            _ = SecItemAdd(insert as CFDictionary, nil)
        }
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
