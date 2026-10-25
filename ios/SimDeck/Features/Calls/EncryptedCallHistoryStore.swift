// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

enum CallHistoryStoreError: Error, Equatable {
    case invalidData
}

struct EncryptedCallHistoryStore {
    private struct Snapshot: Codable {
        var records: [CallRecord]
        var deletedRecordIDs: Set<UUID>

        init(
            records: [CallRecord] = [],
            deletedRecordIDs: Set<UUID> = []
        ) {
            self.records = records
            self.deletedRecordIDs = deletedRecordIDs
        }
    }

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
            let decoded = try load(encryptedData: Data(contentsOf: fileURL), using: vaultKey)
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

    func load(encryptedData: Data, using vaultKey: VaultKey) throws -> [CallRecord] {
        try loadSnapshot(encryptedData: encryptedData, using: vaultKey).records
    }

    func loadSnapshot(using vaultKey: VaultKey) throws -> (records: [CallRecord], deletedRecordIDs: Set<UUID>) {
        guard fileManager.fileExists(atPath: fileURL.path) else {
            return ([], [])
        }
        return try loadSnapshot(encryptedData: Data(contentsOf: fileURL), using: vaultKey)
    }

    func loadSnapshot(encryptedData: Data, using vaultKey: VaultKey) throws -> (records: [CallRecord], deletedRecordIDs: Set<UUID>) {
        do {
            let box = try AES.GCM.SealedBox(combined: encryptedData)
            let plaintext = try AES.GCM.open(
                box,
                using: SymmetricKey(data: vaultKey.data),
                authenticating: Data("sim-deck-call-history-v1".utf8)
            )
            let decoder = JSONDecoder()
            if let snapshot = try? decoder.decode(Snapshot.self, from: plaintext) {
                return (
                    snapshot.records.filter { !snapshot.deletedRecordIDs.contains($0.id) },
                    snapshot.deletedRecordIDs
                )
            }
            // Read snapshots written before deletion tombstones were introduced.
            return (try decoder.decode([CallRecord].self, from: plaintext), [])
        } catch {
            throw CallHistoryStoreError.invalidData
        }
    }

    func merge(encryptedData: Data, using vaultKey: VaultKey) throws {
        let local = try loadSnapshot(using: vaultKey)
        let remote = try loadSnapshot(encryptedData: encryptedData, using: vaultKey)
        let deletedRecordIDs = local.deletedRecordIDs.union(remote.deletedRecordIDs)
        var records = Dictionary(
            uniqueKeysWithValues: local.records
                .filter { !deletedRecordIDs.contains($0.id) }
                .map { ($0.id, $0) }
        )
        for remoteRecord in remote.records where !deletedRecordIDs.contains(remoteRecord.id) {
            if records[remoteRecord.id] == nil {
                records[remoteRecord.id] = remoteRecord
            }
        }
        try saveSnapshot(
            Snapshot(
                records: retainedRecords(from: records.values.sorted { $0.startedAt > $1.startedAt }),
                deletedRecordIDs: deletedRecordIDs
            ),
            using: vaultKey
        )
    }

    func save(_ records: [CallRecord], using vaultKey: VaultKey) throws {
        let deletedRecordIDs = (try? loadSnapshot(using: vaultKey).deletedRecordIDs) ?? []
        try save(
            records,
            deleting: [],
            existingDeletedRecordIDs: deletedRecordIDs,
            using: vaultKey
        )
    }

    func save(
        _ records: [CallRecord],
        deleting recordIDs: Set<UUID>,
        using vaultKey: VaultKey
    ) throws {
        let existingDeletedRecordIDs = (try? loadSnapshot(using: vaultKey).deletedRecordIDs) ?? []
        try save(
            records,
            deleting: recordIDs,
            existingDeletedRecordIDs: existingDeletedRecordIDs,
            using: vaultKey
        )
    }

    private func save(
        _ records: [CallRecord],
        deleting recordIDs: Set<UUID>,
        existingDeletedRecordIDs: Set<UUID>,
        using vaultKey: VaultKey
    ) throws {
        let deletedRecordIDs = existingDeletedRecordIDs.union(recordIDs)
        let snapshot = Snapshot(
            records: retainedRecords(
                from: records.filter { !deletedRecordIDs.contains($0.id) }
            ),
            deletedRecordIDs: deletedRecordIDs
        )
        try saveSnapshot(snapshot, using: vaultKey)
    }

    private func saveSnapshot(_ snapshot: Snapshot, using vaultKey: VaultKey) throws {
        let plaintext = try JSONEncoder().encode(snapshot)
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

    func encryptedData() throws -> Data? {
        guard fileManager.fileExists(atPath: fileURL.path) else { return nil }
        return try Data(contentsOf: fileURL)
    }

    func replaceEncryptedData(_ data: Data) throws {
        let directory = fileURL.deletingLastPathComponent()
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        try data.write(to: fileURL, options: .atomic)
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
