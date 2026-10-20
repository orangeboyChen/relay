import CryptoKit
import Foundation

enum CallHistoryStoreError: Error, Equatable {
    case invalidData
}

struct EncryptedCallHistoryStore {
    private static let retentionInterval: TimeInterval = 365 * 24 * 60 * 60

    private let fileURL: URL
    private let fileManager: FileManager
    private let now: () -> Date

    init(
        fileURL: URL = FileManager.default.urls(
            for: .applicationSupportDirectory,
            in: .userDomainMask
        )[0].appending(path: "SimDeck/calls-v1.bin"),
        fileManager: FileManager = .default,
        now: @escaping () -> Date = Date.init
    ) {
        self.fileURL = fileURL
        self.fileManager = fileManager
        self.now = now
    }

    func load(using vaultKey: VaultKey) throws -> [CallRecord] {
        guard fileManager.fileExists(atPath: fileURL.path) else { return [] }
        do {
            let encrypted = try Data(contentsOf: fileURL)
            let box = try AES.GCM.SealedBox(combined: encrypted)
            let plaintext = try AES.GCM.open(
                box,
                using: SymmetricKey(data: vaultKey.data),
                authenticating: Data("sim-deck-call-history-v1".utf8)
            )
            let decoded = try JSONDecoder().decode([CallRecord].self, from: plaintext)
            let retained = retainedRecords(from: decoded)
            if retained.count != decoded.count {
                try save(retained, using: vaultKey)
            }
            return retained
        } catch let error as CallHistoryStoreError {
            throw error
        } catch {
            throw CallHistoryStoreError.invalidData
        }
    }

    func save(_ records: [CallRecord], using vaultKey: VaultKey) throws {
        let plaintext = try JSONEncoder().encode(retainedRecords(from: records))
        let box = try AES.GCM.seal(
            plaintext,
            using: SymmetricKey(data: vaultKey.data),
            authenticating: Data("sim-deck-call-history-v1".utf8)
        )
        guard let encrypted = box.combined else {
            throw CallHistoryStoreError.invalidData
        }
        let directory = fileURL.deletingLastPathComponent()
        try fileManager.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication]
        )
        try encrypted.write(to: fileURL, options: .atomic)
        try fileManager.setAttributes(
            [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication],
            ofItemAtPath: fileURL.path
        )
    }

    func delete() throws {
        guard fileManager.fileExists(atPath: fileURL.path) else { return }
        try fileManager.removeItem(at: fileURL)
    }

    private func retainedRecords(from records: [CallRecord]) -> [CallRecord] {
        let cutoff = now().addingTimeInterval(-Self.retentionInterval)
        return records
            .filter { ($0.endedAt ?? $0.startedAt) >= cutoff }
            .sorted { $0.startedAt > $1.startedAt }
    }
}
