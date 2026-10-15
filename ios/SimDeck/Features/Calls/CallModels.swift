import Foundation

struct CallRecord: Identifiable, Equatable, Sendable {
    let id: UUID
    let lineID: UUID
    let remoteNumber: String
    let contactName: String?
    let direction: CallDirection
    let result: CallResult
    let startedAt: Date
    let endedAt: Date?

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
}

enum CallDirection: Equatable, Sendable {
    case incoming
    case outgoing
}

enum CallResult: Equatable, Sendable {
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

    var connectedAt: Date? {
        switch self {
        case .active(let connectedAt), .held(let connectedAt):
            connectedAt
        case .requesting, .connecting, .ringing, .ending:
            nil
        }
    }
}
