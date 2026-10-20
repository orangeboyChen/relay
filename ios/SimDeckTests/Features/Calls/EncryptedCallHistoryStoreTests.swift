import Foundation
import XCTest
@testable import SimDeck

final class EncryptedCallHistoryStoreTests: XCTestCase {
    func testHistoryIsEncryptedAndCanBeRestoredWithTheVaultKey() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let fileURL = directory.appending(path: "calls.bin")
        let now = Date(timeIntervalSince1970: 40_000_000)
        let store = EncryptedCallHistoryStore(fileURL: fileURL, now: { now })
        let key = try VaultKey(data: Data(repeating: 7, count: VaultKey.length))
        let record = makeRecord(
            remoteNumber: "13500000000",
            startedAt: now.addingTimeInterval(-120),
            endedAt: now
        )

        try store.save([record], using: key)

        let ciphertext = try Data(contentsOf: fileURL)
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("13500000000"))
        XCTAssertEqual(try store.load(using: key), [record])
    }

    func testHistoryCannotBeOpenedWithAnotherVaultKey() throws {
        let fileURL = FileManager.default.temporaryDirectory.appending(path: "calls-\(UUID()).bin")
        defer { try? FileManager.default.removeItem(at: fileURL) }
        let store = EncryptedCallHistoryStore(fileURL: fileURL)
        let firstKey = try VaultKey(data: Data(repeating: 1, count: VaultKey.length))
        let secondKey = try VaultKey(data: Data(repeating: 2, count: VaultKey.length))

        try store.save([], using: firstKey)

        XCTAssertThrowsError(try store.load(using: secondKey)) {
            XCTAssertEqual($0 as? CallHistoryStoreError, .invalidData)
        }
    }

    func testExpiredRecordsAreRemovedFromTheEncryptedStore() throws {
        let fileURL = FileManager.default.temporaryDirectory.appending(path: "calls-\(UUID()).bin")
        defer { try? FileManager.default.removeItem(at: fileURL) }
        let now = Date(timeIntervalSince1970: 40_000_000)
        let store = EncryptedCallHistoryStore(fileURL: fileURL, now: { now })
        let key = VaultKey.random()
        let retained = makeRecord(
            remoteNumber: "13500000000",
            startedAt: now.addingTimeInterval(-60),
            endedAt: now
        )
        let expired = makeRecord(
            remoteNumber: "13800000000",
            startedAt: now.addingTimeInterval(-(366 * 24 * 60 * 60)),
            endedAt: nil
        )

        try store.save([expired, retained], using: key)

        XCTAssertEqual(try store.load(using: key), [retained])
    }

    func testDeleteRemovesEncryptedHistoryWithoutAffectingAnEmptyStore() throws {
        let fileURL = FileManager.default.temporaryDirectory.appending(path: "calls-\(UUID()).bin")
        defer { try? FileManager.default.removeItem(at: fileURL) }
        let store = EncryptedCallHistoryStore(fileURL: fileURL)

        try store.save([], using: .random())
        XCTAssertTrue(FileManager.default.fileExists(atPath: fileURL.path))

        try store.delete()
        XCTAssertFalse(FileManager.default.fileExists(atPath: fileURL.path))
        try store.delete()
    }

    private func makeRecord(
        remoteNumber: String,
        startedAt: Date,
        endedAt: Date?
    ) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: UUID(),
            lineSnapshot: LineHistorySnapshot(
                numericID: 2,
                lineName: "Primary",
                phoneNumber: "13500000000"
            ),
            remoteNumber: remoteNumber,
            contactName: "Home",
            direction: .incoming,
            result: .answered,
            startedAt: startedAt,
            endedAt: endedAt
        )
    }
}
