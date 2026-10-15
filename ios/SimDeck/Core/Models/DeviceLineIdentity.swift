import Foundation

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
