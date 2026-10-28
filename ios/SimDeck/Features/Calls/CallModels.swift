// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct CallRecord: Identifiable, Equatable, Sendable, Codable {
    let id: UUID
    let lineID: UUID
    var lineSnapshot: LineHistorySnapshot?
    let remoteNumber: String
    let contactName: String?
    let direction: CallDirection
    let result: CallResult
    var isRead: Bool
    let startedAt: Date
    let endedAt: Date?

    init(
        id: UUID,
        lineID: UUID,
        lineSnapshot: LineHistorySnapshot? = nil,
        remoteNumber: String,
        contactName: String?,
        direction: CallDirection,
        result: CallResult,
        isRead: Bool? = nil,
        startedAt: Date,
        endedAt: Date?
    ) {
        self.id = id
        self.lineID = lineID
        self.lineSnapshot = lineSnapshot
        self.remoteNumber = remoteNumber
        self.contactName = contactName
        self.direction = direction
        self.result = result
        self.isRead = isRead ?? (result != .missed)
        self.startedAt = startedAt
        self.endedAt = endedAt
    }

    private enum CodingKeys: String, CodingKey {
        case id
        case lineID
        case lineSnapshot
        case remoteNumber
        case contactName
        case direction
        case result
        case isRead
        case startedAt
        case endedAt
    }

    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UUID.self, forKey: .id)
        lineID = try container.decode(UUID.self, forKey: .lineID)
        lineSnapshot = try container.decodeIfPresent(
            LineHistorySnapshot.self,
            forKey: .lineSnapshot
        )
        remoteNumber = try container.decode(String.self, forKey: .remoteNumber)
        contactName = try container.decodeIfPresent(String.self, forKey: .contactName)
        direction = try container.decode(CallDirection.self, forKey: .direction)
        result = try container.decode(CallResult.self, forKey: .result)
        isRead = try container.decodeIfPresent(Bool.self, forKey: .isRead) ?? true
        startedAt = try container.decode(Date.self, forKey: .startedAt)
        endedAt = try container.decodeIfPresent(Date.self, forKey: .endedAt)
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(lineID, forKey: .lineID)
        try container.encodeIfPresent(lineSnapshot, forKey: .lineSnapshot)
        try container.encode(remoteNumber, forKey: .remoteNumber)
        try container.encodeIfPresent(contactName, forKey: .contactName)
        try container.encode(direction, forKey: .direction)
        try container.encode(result, forKey: .result)
        try container.encode(isRead, forKey: .isRead)
        try container.encode(startedAt, forKey: .startedAt)
        try container.encodeIfPresent(endedAt, forKey: .endedAt)
    }

    var displayName: String {
        guard let contactName, !contactName.isEmpty else {
            return remoteNumber
        }
        return contactName
    }

    var duration: TimeInterval? {
        guard let endedAt else { return nil }
        return max(0, endedAt.timeIntervalSince(startedAt))
    }

    func linePresentation(in lines: [DeviceLineIdentity]) -> LineHistoryPresentation {
        LineHistoryPresentation(
            line: lines.first(where: { $0.id == lineID }),
            snapshot: lineSnapshot
        )
    }
}

enum CallDirection: Equatable, Sendable, Codable {
    case incoming
    case outgoing
}

enum CallResult: Equatable, Sendable, Codable {
    case answered
    case missed
    case rejected
    case failed
}

enum CallHistoryState: Equatable, Sendable {
    case loading
    case loaded([CallRecord])
    case failed
}

struct ActiveCallSession: Identifiable, Equatable, Sendable {
    let id: UUID
    let lineID: UUID
    let remoteNumber: String
    var displayName: String?
    var status: ActiveCallStatus
    var isMuted: Bool

    var title: String {
        guard let displayName, !displayName.isEmpty else { return remoteNumber }
        return displayName
    }
}

enum ActiveCallStatus: Equatable, Sendable {
    case requesting
    case connecting
    case ringing
    case active(connectedAt: Date)
    case held(connectedAt: Date)
    case ending
    case ended
    case failed

    var connectedAt: Date? {
        switch self {
        case .active(let connectedAt), .held(let connectedAt):
            connectedAt
        case .requesting, .connecting, .ringing, .ending, .ended, .failed:
            nil
        }
    }

    var isEnded: Bool {
        self == .ended
    }

    var isHeld: Bool {
        if case .held = self { return true }
        return false
    }
}
