// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

enum MessageHistoryStoreError: Error, Equatable {
    case invalidData
}

struct EncryptedMessageHistoryStore {
    private struct Snapshot: Codable {
        var conversations: [MessageConversation]
        var deletedMessageIDs: Set<UUID>

        init(
            conversations: [MessageConversation] = [],
            deletedMessageIDs: Set<UUID> = []
        ) {
            self.conversations = conversations
            self.deletedMessageIDs = deletedMessageIDs
        }
    }

    private let fileURL: URL
    private let fileManager: FileManager

    init(
        fileURL: URL = FileManager.default.urls(
            for: .applicationSupportDirectory,
            in: .userDomainMask
        )[0].appending(path: "SimDeck/messages-v1.bin"),
        fileManager: FileManager = .default
    ) {
        self.fileURL = fileURL
        self.fileManager = fileManager
    }

    func load(using vaultKey: VaultKey) throws -> [MessageConversation] {
        guard fileManager.fileExists(atPath: fileURL.path) else { return [] }
        do {
            return try loadSnapshot(using: vaultKey).conversations
        } catch {
            throw MessageHistoryStoreError.invalidData
        }
    }

    func load(encryptedData: Data, using vaultKey: VaultKey) throws -> [MessageConversation] {
        try loadSnapshot(encryptedData: encryptedData, using: vaultKey).conversations
    }

    func loadSnapshot(using vaultKey: VaultKey) throws -> (conversations: [MessageConversation], deletedMessageIDs: Set<UUID>) {
        guard fileManager.fileExists(atPath: fileURL.path) else {
            return ([], [])
        }
        return try loadSnapshot(encryptedData: Data(contentsOf: fileURL), using: vaultKey)
    }

    func loadSnapshot(encryptedData: Data, using vaultKey: VaultKey) throws -> (conversations: [MessageConversation], deletedMessageIDs: Set<UUID>) {
        do {
            let box = try AES.GCM.SealedBox(combined: encryptedData)
            let plaintext = try AES.GCM.open(
                box,
                using: SymmetricKey(data: vaultKey.data),
                authenticating: Data("sim-deck-message-history-v1".utf8)
            )
            let decoder = JSONDecoder()
            if let snapshot = try? decoder.decode(Snapshot.self, from: plaintext) {
                let conversations = snapshot.conversations.compactMap { conversation in
                    let filteredConversation = filtered(
                        conversation,
                        deleting: snapshot.deletedMessageIDs
                    )
                    return filteredConversation.messages.isEmpty ? nil : filteredConversation
                }
                return (conversations, snapshot.deletedMessageIDs)
            }
            // Read snapshots written before deletion tombstones were introduced.
            return (try decoder.decode([MessageConversation].self, from: plaintext), [])
        } catch {
            throw MessageHistoryStoreError.invalidData
        }
    }

    func deletedMessageIDs(using vaultKey: VaultKey) -> Set<UUID> {
        (try? loadSnapshot(using: vaultKey).deletedMessageIDs) ?? []
    }

    func merge(encryptedData: Data, using vaultKey: VaultKey) throws {
        let local = try loadSnapshot(using: vaultKey)
        let remote = try loadSnapshot(encryptedData: encryptedData, using: vaultKey)
        let deletedMessageIDs = local.deletedMessageIDs.union(remote.deletedMessageIDs)
        var conversations = Dictionary(
            uniqueKeysWithValues: local.conversations.compactMap { conversation in
                let filteredConversation = filtered(conversation, deleting: deletedMessageIDs)
                return filteredConversation.messages.isEmpty
                    ? nil
                    : (filteredConversation.id, filteredConversation)
            }
        )
        for remoteConversation in remote.conversations {
            let remote = filtered(remoteConversation, deleting: deletedMessageIDs)
            guard !remote.messages.isEmpty else { continue }
            guard var local = conversations[remote.id] else {
                conversations[remote.id] = remote
                continue
            }
            var messages = Dictionary(uniqueKeysWithValues: local.messages.map { ($0.id, $0) })
            for message in remote.messages {
                if let existing = messages[message.id] {
                    if existing.status == .sending && message.status != .sending {
                        messages[message.id] = message
                    }
                } else {
                    messages[message.id] = message
                }
            }
            local.messages = messages.values.sorted { $0.sentAt < $1.sentAt }
            local.unreadCount = max(local.unreadCount, remote.unreadCount)
            if local.contactName == nil { local.contactName = remote.contactName }
            if local.lineSnapshot == nil { local.lineSnapshot = remote.lineSnapshot }
            local.messages = local.messages.filter { !deletedMessageIDs.contains($0.id) }
            if local.messages.isEmpty {
                conversations.removeValue(forKey: remote.id)
            } else {
                conversations[remote.id] = local
            }
        }
        try saveSnapshot(Snapshot(
            conversations: conversations.values.filter { !$0.messages.isEmpty }.sorted {
            ($0.messages.last?.sentAt ?? .distantPast) > ($1.messages.last?.sentAt ?? .distantPast)
            },
            deletedMessageIDs: deletedMessageIDs
        ), using: vaultKey)
    }

    func save(_ conversations: [MessageConversation], using vaultKey: VaultKey) throws {
        let deletedMessageIDs = (try? loadSnapshot(using: vaultKey).deletedMessageIDs) ?? []
        try save(
            conversations,
            deleting: [],
            existingDeletedMessageIDs: deletedMessageIDs,
            using: vaultKey
        )
    }

    func save(
        _ conversations: [MessageConversation],
        deleting messageIDs: Set<UUID>,
        using vaultKey: VaultKey
    ) throws {
        let existingDeletedMessageIDs = (try? loadSnapshot(using: vaultKey).deletedMessageIDs) ?? []
        try save(
            conversations,
            deleting: messageIDs,
            existingDeletedMessageIDs: existingDeletedMessageIDs,
            using: vaultKey
        )
    }

    private func save(
        _ conversations: [MessageConversation],
        deleting messageIDs: Set<UUID>,
        existingDeletedMessageIDs: Set<UUID>,
        using vaultKey: VaultKey
    ) throws {
        let deletedMessageIDs = existingDeletedMessageIDs.union(messageIDs)
        let snapshot = Snapshot(
            conversations: conversations.compactMap { conversation in
                let filteredConversation = filtered(conversation, deleting: deletedMessageIDs)
                return filteredConversation.messages.isEmpty ? nil : filteredConversation
            },
            deletedMessageIDs: deletedMessageIDs
        )
        try saveSnapshot(snapshot, using: vaultKey)
    }

    private func saveSnapshot(_ snapshot: Snapshot, using vaultKey: VaultKey) throws {
        let plaintext = try JSONEncoder().encode(snapshot)
        let box = try AES.GCM.seal(
            plaintext,
            using: SymmetricKey(data: vaultKey.data),
            authenticating: Data("sim-deck-message-history-v1".utf8)
        )
        guard let encrypted = box.combined else {
            throw MessageHistoryStoreError.invalidData
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

    private func filtered(
        _ conversation: MessageConversation,
        deleting messageIDs: Set<UUID>
    ) -> MessageConversation {
        var conversation = conversation
        conversation.messages.removeAll { messageIDs.contains($0.id) }
        conversation.unreadCount = min(conversation.unreadCount, conversation.messages.count)
        return conversation
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
}
