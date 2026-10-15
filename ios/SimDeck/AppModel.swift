import Foundation
import Observation

struct DeviceLineIdentity: Identifiable, Equatable, Sendable {
    let id: UUID
    var numericID: Int
    var deviceName: String
    var lineName: String
    var phoneNumber: String
    var isOnline: Bool

    var displayName: String {
        if !phoneNumber.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return phoneNumber
        }
        if !lineName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return lineName
        }
        return "SIM \(numericID)"
    }
}

@MainActor
@Observable
final class AppModel {
    var devices: [DeviceLineIdentity]
    var callHistory: CallHistoryState
    var lastSuccessfulDialLineID: UUID?
    var selectedTab: AppTab
    let callStarter: any CallStarting

    init(
        devices: [DeviceLineIdentity] = [],
        callHistory: CallHistoryState = .loaded([]),
        lastSuccessfulDialLineID: UUID? = nil,
        selectedTab: AppTab = .calls,
        callStarter: any CallStarting = CallKitCallStarter()
    ) {
        self.devices = devices
        self.callHistory = callHistory
        self.lastSuccessfulDialLineID = lastSuccessfulDialLineID
        self.selectedTab = selectedTab
        self.callStarter = callStarter
    }
}

enum AppTab: Hashable {
    case calls
    case messages
    case profile
}

#if DEBUG
extension AppModel {
    static func preview(selectedTab: AppTab = .calls) -> AppModel {
        AppModel(
            devices: PreviewFixtures.devices,
            callHistory: .loaded(PreviewFixtures.calls),
            lastSuccessfulDialLineID: PreviewFixtures.devices[0].id,
            selectedTab: selectedTab,
            callStarter: PreviewCallStarter()
        )
    }
}

enum PreviewFixtures {
    static let now = Date(timeIntervalSince1970: 1_797_000_000)

    static let devices = [
        DeviceLineIdentity(
            id: UUID(uuidString: "00000000-0000-0000-0000-000000000001")!,
            numericID: 1,
            deviceName: "Office EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true
        ),
        DeviceLineIdentity(
            id: UUID(uuidString: "00000000-0000-0000-0000-000000000002")!,
            numericID: 2,
            deviceName: "Backup modem with a deliberately long management name",
            lineName: "Backup",
            phoneNumber: "",
            isOnline: false
        )
    ]

    static let calls = [
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000001")!,
            lineID: devices[0].id,
            remoteNumber: "10086",
            contactName: "China Mobile",
            direction: .incoming,
            result: .answered,
            startedAt: now.addingTimeInterval(-420),
            endedAt: now.addingTimeInterval(-301)
        ),
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000002")!,
            lineID: devices[1].id,
            remoteNumber: "13800138000",
            contactName: nil,
            direction: .incoming,
            result: .missed,
            startedAt: now.addingTimeInterval(-86_400),
            endedAt: nil
        ),
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000003")!,
            lineID: devices[0].id,
            remoteNumber: "0755 1234 5678",
            contactName: "Service Desk",
            direction: .outgoing,
            result: .answered,
            startedAt: now.addingTimeInterval(-172_800),
            endedAt: now.addingTimeInterval(-172_134)
        )
    ]
}
#endif
