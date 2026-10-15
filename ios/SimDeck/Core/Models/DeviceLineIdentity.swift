import Foundation

struct DeviceLineIdentity: Identifiable, Equatable, Sendable {
    let id: UUID
    var numericID: Int
    var deviceName: String
    var lineName: String
    var phoneNumber: String
    var isOnline: Bool
    var commandPublicKey: Data?

    init(
        id: UUID,
        numericID: Int,
        deviceName: String,
        lineName: String,
        phoneNumber: String,
        isOnline: Bool
    ) {
        self.init(
            id: id,
            numericID: numericID,
            deviceName: deviceName,
            lineName: lineName,
            phoneNumber: phoneNumber,
            isOnline: isOnline,
            commandPublicKey: nil
        )
    }

    init(
        id: UUID,
        numericID: Int,
        deviceName: String,
        lineName: String,
        phoneNumber: String,
        isOnline: Bool,
        commandPublicKey: Data?
    ) {
        self.id = id
        self.numericID = numericID
        self.deviceName = deviceName
        self.lineName = lineName
        self.phoneNumber = phoneNumber
        self.isOnline = isOnline
        self.commandPublicKey = commandPublicKey
    }

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
