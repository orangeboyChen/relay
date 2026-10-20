// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

final class EncryptedMessageHistoryStoreTests: XCTestCase {
    func testHistoryIsEncryptedAndCanBeRestoredWithTheVaultKey() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let fileURL = directory.appending(path: "messages.bin")
        let store = EncryptedMessageHistoryStore(fileURL: fileURL)
        let key = try VaultKey(data: Data(repeating: 7, count: VaultKey.length))
        let conversation = MessageConversation(
            lineID: UUID(),
            remoteNumber: "13500000000",
            contactName: nil,
            unreadCount: 1,
            messages: [
                MessageItem(
                    id: UUID(),
                    sourceID: String(repeating: "a", count: 64),
                    direction: .received,
                    body: "private SMS body",
                    attachment: nil,
                    sentAt: Date(timeIntervalSince1970: 100),
                    status: .delivered
                ),
            ]
        )

        try store.save([conversation], using: key)

        let ciphertext = try Data(contentsOf: fileURL)
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("private SMS body"))
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("13500000000"))
        XCTAssertEqual(try store.load(using: key), [conversation])
    }

    func testHistoryCannotBeOpenedWithAnotherVaultKey() throws {
        let fileURL = FileManager.default.temporaryDirectory.appending(path: "messages-\(UUID()).bin")
        defer { try? FileManager.default.removeItem(at: fileURL) }
        let store = EncryptedMessageHistoryStore(fileURL: fileURL)
        let firstKey = try VaultKey(data: Data(repeating: 1, count: VaultKey.length))
        let secondKey = try VaultKey(data: Data(repeating: 2, count: VaultKey.length))

        try store.save([], using: firstKey)

        XCTAssertThrowsError(try store.load(using: secondKey)) {
            XCTAssertEqual($0 as? MessageHistoryStoreError, .invalidData)
        }
    }

    func testLegacyHistoryWithoutLineSnapshotStillDecodes() throws {
        let conversation = MessageConversation(
            lineID: UUID(),
            remoteNumber: "13500000000",
            contactName: nil,
            unreadCount: 0,
            messages: []
        )

        let encoded = try JSONEncoder().encode([conversation])
        let decoded = try JSONDecoder().decode([MessageConversation].self, from: encoded)

        XCTAssertEqual(decoded, [conversation])
        XCTAssertNil(decoded[0].lineSnapshot)
    }

    func testDeleteRemovesEncryptedHistoryWithoutAffectingAnEmptyStore() throws {
        let fileURL = FileManager.default.temporaryDirectory.appending(path: "messages-\(UUID()).bin")
        defer { try? FileManager.default.removeItem(at: fileURL) }
        let store = EncryptedMessageHistoryStore(fileURL: fileURL)
        let key = VaultKey.random()

        try store.save([], using: key)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fileURL.path))

        try store.delete()
        XCTAssertFalse(FileManager.default.fileExists(atPath: fileURL.path))
        try store.delete()
    }
}
