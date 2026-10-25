// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import CryptoKit
import RelayProtocol

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

/// Persists eSIM profile metadata through the authenticated Owner RPC.
/// The modem remains responsible for actual eSIM provisioning; this manager
/// never stores an activation code on the server.
@MainActor
final class LiveESIMProfileManager: ESIMProfileManaging {
    private let server: ServerConnectionManager

    init(server: ServerConnectionManager) {
        self.server = server
    }

    func loadProfiles(line: DeviceLineIdentity) async throws -> [ESIMProfile] {
        let simID = try requireSIMID(line)
        return try await server.listESIMProfiles(simID: simID).map { makeProfile($0) }
    }

    func setEnabled(_ enabled: Bool, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let updated = try await server.setESIMProfileEnabled(
            simID: requireSIMID(line), profileID: profile.profileID, enabled: enabled
        )
        return makeProfile(updated, preservingID: profile.id)
    }

    func rename(_ name: String, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let updated = try await server.renameESIMProfile(
            simID: requireSIMID(line), profileID: profile.profileID, name: name
        )
        return makeProfile(updated, preservingID: profile.id)
    }

    func delete(profile: ESIMProfile, line: DeviceLineIdentity) async throws {
        try await server.deleteESIMProfile(simID: requireSIMID(line), profileID: profile.profileID)
    }

    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let trimmedCode = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedCode.isEmpty else { throw ESIMProfileManagerError.invalidActivationCode }
        let digest = SHA256.hash(data: Data(trimmedCode.utf8))
        let profileID = digest.prefix(16).map { String(format: "%02x", $0) }.joined()
        let profile = try await server.addESIMProfile(
            simID: requireSIMID(line), profileID: profileID
        )
        return makeProfile(profile)
    }

    private func requireSIMID(_ line: DeviceLineIdentity) throws -> UUID {
        guard line.simKind == .eSIM, let simID = line.simID else {
            throw ESIMProfileManagerError.unsupported
        }
        return simID
    }

    private func makeProfile(_ profile: Relay_Communication_V1_EsimProfile, preservingID: UUID? = nil) -> ESIMProfile {
        ESIMProfile(
            id: preservingID ?? stableID(for: profile.profileID),
            profileID: profile.profileID,
            name: profile.name,
            iccid: profile.iccid,
            operatorName: profile.operatorName,
            isEnabled: profile.isEnabled
        )
    }

    private func stableID(for profileID: String) -> UUID {
        let bytes = Array(SHA256.hash(data: Data(profileID.utf8)).prefix(16))
        var uuidBytes = bytes
        uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x50
        uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
        return UUID(uuid: (
            uuidBytes[0], uuidBytes[1], uuidBytes[2], uuidBytes[3],
            uuidBytes[4], uuidBytes[5], uuidBytes[6], uuidBytes[7],
            uuidBytes[8], uuidBytes[9], uuidBytes[10], uuidBytes[11],
            uuidBytes[12], uuidBytes[13], uuidBytes[14], uuidBytes[15]
        ))
    }
}

