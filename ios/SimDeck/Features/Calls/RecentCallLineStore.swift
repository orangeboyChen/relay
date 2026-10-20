// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

@MainActor
protocol RecentCallLineStoring: AnyObject {
    func record(callID: UUID, remoteNumber: String, lineID: UUID)
    func lineID(callRecordIdentifier: String?, remoteNumber: String) -> UUID?
}

@MainActor
final class UserDefaultsRecentCallLineStore: RecentCallLineStoring {
    private struct Entry: Codable {
        let callID: UUID
        let numberFingerprint: String
        let lineID: UUID
        let recordedAt: Date
    }

    private enum Storage {
        static let key = "sim-deck.recent-call-line-routes.v1"
        static let retentionInterval: TimeInterval = 365 * 24 * 60 * 60
        static let maximumEntries = 512
    }

    private let defaults: UserDefaults
    private let now: () -> Date

    init(
        defaults: UserDefaults = .standard,
        now: @escaping () -> Date = Date.init
    ) {
        self.defaults = defaults
        self.now = now
    }

    func record(callID: UUID, remoteNumber: String, lineID: UUID) {
        guard let fingerprint = Self.fingerprint(for: remoteNumber) else { return }

        var entries = retainedEntries()
        entries.removeAll(where: { $0.callID == callID })
        entries.append(
            Entry(
                callID: callID,
                numberFingerprint: fingerprint,
                lineID: lineID,
                recordedAt: now()
            )
        )
        entries.sort(by: { $0.recordedAt > $1.recordedAt })
        persist(Array(entries.prefix(Storage.maximumEntries)))
    }

    func lineID(callRecordIdentifier: String?, remoteNumber: String) -> UUID? {
        let entries = retainedEntries()
        if let callRecordIdentifier,
           let callID = UUID(uuidString: callRecordIdentifier),
           let exactMatch = entries.first(where: { $0.callID == callID }) {
            return exactMatch.lineID
        }
        guard let fingerprint = Self.fingerprint(for: remoteNumber) else { return nil }
        return entries.first(where: { $0.numberFingerprint == fingerprint })?.lineID
    }

    private func retainedEntries() -> [Entry] {
        guard let data = defaults.data(forKey: Storage.key),
              let decoded = try? JSONDecoder().decode([Entry].self, from: data) else {
            return []
        }
        let cutoff = now().addingTimeInterval(-Storage.retentionInterval)
        return decoded
            .filter { $0.recordedAt >= cutoff }
            .sorted(by: { $0.recordedAt > $1.recordedAt })
    }

    private func persist(_ entries: [Entry]) {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        defaults.set(data, forKey: Storage.key)
    }

    private static func fingerprint(for remoteNumber: String) -> String? {
        let normalizedNumber = DialString.normalized(remoteNumber)
        guard DialString.isValid(normalizedNumber) else { return nil }
        return SHA256.hash(data: Data(normalizedNumber.utf8))
            .map { String(format: "%02x", $0) }
            .joined()
    }
}

@MainActor
final class NullRecentCallLineStore: RecentCallLineStoring {
    func record(callID: UUID, remoteNumber: String, lineID: UUID) {}

    func lineID(callRecordIdentifier: String?, remoteNumber: String) -> UUID? {
        nil
    }
}
