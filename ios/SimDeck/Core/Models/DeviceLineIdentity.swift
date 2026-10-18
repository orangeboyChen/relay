import Foundation

enum CellularRadioAccessTechnology: String, Codable, Equatable, Sendable {
    case unknown
    case gsm
    case edge
    case umts
    case hspa
    case lte
    case nrNSA = "nr_nsa"
    case nrSA = "nr_sa"

    var displayName: String {
        switch self {
        case .unknown:
            String(localized: "Unknown")
        case .gsm:
            "2G"
        case .edge:
            "EDGE"
        case .umts:
            "3G"
        case .hspa:
            "H+"
        case .lte:
            "LTE"
        case .nrNSA, .nrSA:
            "5G"
        }
    }
}

struct CellularRadioStatus: Equatable, Sendable {
    let technology: CellularRadioAccessTechnology
    let signalBars: Int?

    init(technology: CellularRadioAccessTechnology, signalBars: Int?) {
        self.technology = technology
        self.signalBars = signalBars.flatMap { (0...4).contains($0) ? $0 : nil }
    }

    var signalAccessibilityDescription: String {
        guard let signalBars else {
            return String(localized: "Signal unavailable")
        }
        return String(localized: "\(signalBars) of 4 signal bars")
    }
}

struct DeviceLineIdentity: Identifiable, Equatable, Sendable {
    let id: UUID
    let deviceID: UUID
    let slotID: UUID?
    let simID: UUID?
    var numericID: Int
    var slotKey: String?
    var slotIndex: Int?
    var deviceName: String
    var lineName: String
    var phoneNumber: String
    var isOnline: Bool
    var radioStatus: CellularRadioStatus?
    var commandPublicKey: Data?

    init(
        id: UUID,
        numericID: Int,
        slotKey: String? = nil,
        slotIndex: Int? = nil,
        deviceName: String,
        lineName: String,
        phoneNumber: String,
        isOnline: Bool,
        radioStatus: CellularRadioStatus? = nil
    ) {
        self.init(
            id: id,
            deviceID: id,
            slotID: nil,
            simID: nil,
            numericID: numericID,
            slotKey: slotKey,
            slotIndex: slotIndex,
            deviceName: deviceName,
            lineName: lineName,
            phoneNumber: phoneNumber,
            isOnline: isOnline,
            radioStatus: radioStatus,
            commandPublicKey: nil
        )
    }

    init(
        id: UUID,
        deviceID: UUID? = nil,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        numericID: Int,
        slotKey: String? = nil,
        slotIndex: Int? = nil,
        deviceName: String,
        lineName: String,
        phoneNumber: String,
        isOnline: Bool,
        radioStatus: CellularRadioStatus? = nil,
        commandPublicKey: Data?
    ) {
        self.id = simID ?? slotID ?? id
        self.deviceID = deviceID ?? id
        self.slotID = slotID
        self.simID = simID
        self.numericID = numericID
        self.slotKey = slotKey
        self.slotIndex = slotIndex
        self.deviceName = deviceName
        self.lineName = lineName
        self.phoneNumber = phoneNumber
        self.isOnline = isOnline
        self.radioStatus = radioStatus
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

    var radioAccessibilityDescription: String {
        guard isOnline else {
            return String(localized: "Offline")
        }
        guard let radioStatus else {
            return String(localized: "Online, signal unavailable")
        }
        return "\(radioStatus.technology.displayName), \(radioStatus.signalAccessibilityDescription)"
    }
}
