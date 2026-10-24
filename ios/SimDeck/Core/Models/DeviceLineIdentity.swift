// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum SIMCardKind: String, Codable, Equatable, Sendable {
    case physical
    case eSIM

    var displayName: String {
        switch self {
        case .physical:
            String(localized: "Physical SIM")
        case .eSIM:
            String(localized: "eSIM")
        }
    }
}

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
    var simKind: SIMCardKind

    init(
        id: UUID,
        numericID: Int,
        slotKey: String? = nil,
        slotIndex: Int? = nil,
        deviceName: String,
        lineName: String,
        phoneNumber: String,
        isOnline: Bool,
        radioStatus: CellularRadioStatus? = nil,
        simKind: SIMCardKind = .physical
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
            commandPublicKey: nil,
            simKind: simKind
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
        commandPublicKey: Data?,
        simKind: SIMCardKind = .physical
    ) {
        // A slot is the stable routing identity for a physical modem position.
        // The SIM identity is independently managed and may be moved or replaced.
        self.id = slotID ?? simID ?? id
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
        self.simKind = simKind
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

struct LineHistorySnapshot: Codable, Equatable, Sendable {
    let numericID: Int
    let lineName: String
    let phoneNumber: String

    init(numericID: Int, lineName: String, phoneNumber: String) {
        self.numericID = numericID
        self.lineName = lineName
        self.phoneNumber = phoneNumber
    }

    init(line: DeviceLineIdentity) {
        self.init(
            numericID: line.numericID,
            lineName: line.lineName,
            phoneNumber: line.phoneNumber
        )
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

struct LineHistoryPresentation: Equatable, Sendable {
    let snapshot: LineHistorySnapshot?
    let isAvailable: Bool
    let isOnline: Bool

    init(line: DeviceLineIdentity?, snapshot: LineHistorySnapshot?) {
        self.snapshot = line.map(LineHistorySnapshot.init(line:)) ?? snapshot
        isAvailable = line != nil
        isOnline = line?.isOnline == true
    }

    var displayName: String {
        snapshot?.displayName ?? String(localized: "Unavailable")
    }
}
