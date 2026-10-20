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
        self.startedAt = startedAt
        self.endedAt = endedAt
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
    case failed

    var connectedAt: Date? {
        switch self {
        case .active(let connectedAt), .held(let connectedAt):
            connectedAt
        case .requesting, .connecting, .ringing, .ending, .failed:
            nil
        }
    }
}
