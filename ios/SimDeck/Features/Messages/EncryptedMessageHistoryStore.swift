// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

enum MessageHistoryStoreError: Error, Equatable {
    case invalidData
}

struct EncryptedMessageHistoryStore {
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
            return try load(encryptedData: Data(contentsOf: fileURL), using: vaultKey)
        } catch {
            throw MessageHistoryStoreError.invalidData
        }
    }

    func load(encryptedData: Data, using vaultKey: VaultKey) throws -> [MessageConversation] {
        do {
            let box = try AES.GCM.SealedBox(combined: encryptedData)
            let plaintext = try AES.GCM.open(
                box,
                using: SymmetricKey(data: vaultKey.data),
                authenticating: Data("sim-deck-message-history-v1".utf8)
            )
            return try JSONDecoder().decode([MessageConversation].self, from: plaintext)
        } catch {
            throw MessageHistoryStoreError.invalidData
        }
    }

    func merge(encryptedData: Data, using vaultKey: VaultKey) throws {
        var conversations = Dictionary(uniqueKeysWithValues: try load(using: vaultKey).map { ($0.id, $0) })
        for remote in try load(encryptedData: encryptedData, using: vaultKey) {
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
            conversations[remote.id] = local
        }
        try save(conversations.values.sorted {
            ($0.messages.last?.sentAt ?? .distantPast) > ($1.messages.last?.sentAt ?? .distantPast)
        }, using: vaultKey)
    }

    func save(_ conversations: [MessageConversation], using vaultKey: VaultKey) throws {
        let plaintext = try JSONEncoder().encode(conversations)
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
