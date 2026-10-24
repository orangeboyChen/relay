// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct ESIMProfile: Identifiable, Codable, Equatable, Sendable {
    let id: UUID
    var profileID: String
    var name: String
    var iccid: String
    var operatorName: String
    var activationCode: String
    var isEnabled: Bool

    init(
        id: UUID = UUID(),
        profileID: String,
        name: String = "",
        iccid: String = "",
        operatorName: String = "",
        activationCode: String = "",
        isEnabled: Bool = false
    ) {
        self.id = id
        self.profileID = profileID
        self.name = name
        self.iccid = iccid
        self.operatorName = operatorName
        self.activationCode = activationCode
        self.isEnabled = isEnabled
    }

    var displayName: String {
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedName.isEmpty { return trimmedName }
        let trimmedProfileID = profileID.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedProfileID.isEmpty { return trimmedProfileID }
        return iccid.isEmpty ? String(localized: "Unnamed eSIM") : iccid
    }

    var detailSubtitle: String {
        let values = [operatorName, iccid].filter { !$0.isEmpty }
        return values.isEmpty ? profileID : values.joined(separator: " · ")
    }
}

enum ESIMProfileManagerError: LocalizedError, Equatable {
    case unsupported
    case unavailable
    case invalidActivationCode
    case duplicateProfile

    var errorDescription: String? {
        switch self {
        case .unsupported:
            String(localized: "This modem does not expose eSIM profile management.")
        case .unavailable:
            String(localized: "The eSIM device is offline.")
        case .invalidActivationCode:
            String(localized: "Enter a valid eSIM activation code.")
        case .duplicateProfile:
            String(localized: "This eSIM profile is already installed.")
        }
    }
}

@MainActor
protocol ESIMProfileManaging: AnyObject {
    func loadProfiles(line: DeviceLineIdentity) async throws -> [ESIMProfile]
    func setEnabled(_ enabled: Bool, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile
    func rename(_ name: String, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile
    func delete(profile: ESIMProfile, line: DeviceLineIdentity) async throws
    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> ESIMProfile
}

@MainActor
final class UnavailableESIMProfileManager: ESIMProfileManaging {
    func loadProfiles(line: DeviceLineIdentity) async throws -> [ESIMProfile] {
        guard line.simKind == .eSIM else { return [] }
        throw ESIMProfileManagerError.unsupported
    }

    func setEnabled(_ enabled: Bool, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        throw ESIMProfileManagerError.unsupported
    }

    func rename(_ name: String, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        throw ESIMProfileManagerError.unsupported
    }

    func delete(profile: ESIMProfile, line: DeviceLineIdentity) async throws {
        throw ESIMProfileManagerError.unsupported
    }

    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> ESIMProfile {
        throw ESIMProfileManagerError.unsupported
    }
}

