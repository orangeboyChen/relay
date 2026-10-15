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
    var selectedTab: AppTab

    init(
        devices: [DeviceLineIdentity] = [],
        selectedTab: AppTab = .calls
    ) {
        self.devices = devices
        self.selectedTab = selectedTab
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
            selectedTab: selectedTab
        )
    }
}

enum PreviewFixtures {
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
}
#endif
