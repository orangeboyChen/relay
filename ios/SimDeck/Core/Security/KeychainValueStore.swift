import Foundation
import Security

struct KeychainValueStore: SecureValueStoring {
    let service: String
    let account: String
    let synchronizable: Bool
    let accessibility: String

    func read() throws -> Data? {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw KeyRecoveryError.keychain(status)
        }
        return data
    }

    func write(_ value: Data) throws {
        var attributes = baseQuery
        attributes[kSecValueData as String] = value
        attributes[kSecAttrAccessible as String] = accessibility
        let status = SecItemAdd(attributes as CFDictionary, nil)
        if status == errSecDuplicateItem {
            let updateStatus = SecItemUpdate(
                baseQuery as CFDictionary,
                [
                    kSecValueData as String: value,
                    kSecAttrAccessible as String: accessibility,
                ] as CFDictionary
            )
            guard updateStatus == errSecSuccess else {
                throw KeyRecoveryError.keychain(updateStatus)
            }
            return
        }
        guard status == errSecSuccess else {
            throw KeyRecoveryError.keychain(status)
        }
    }

    func delete() throws {
        let status = SecItemDelete(baseQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeyRecoveryError.keychain(status)
        }
    }

    private var baseQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrSynchronizable as String: synchronizable,
        ]
    }
}

struct UserDefaultsInstallMarkerStore: InstallMarkerStoring {
    private let key = "sim-deck.installation-marker.v1"

    func isInstalled() -> Bool {
        UserDefaults.standard.bool(forKey: key)
    }

    func markInstalled() {
        UserDefaults.standard.set(true, forKey: key)
    }
}
