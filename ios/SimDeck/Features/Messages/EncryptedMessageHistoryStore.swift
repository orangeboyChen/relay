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
            let encrypted = try Data(contentsOf: fileURL)
            let box = try AES.GCM.SealedBox(combined: encrypted)
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

    func delete() throws {
        guard fileManager.fileExists(atPath: fileURL.path) else { return }
        try fileManager.removeItem(at: fileURL)
    }
}
