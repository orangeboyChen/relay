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

/// Modem-owned profile data returned through an encrypted device command.
/// This is intentionally separate from server-owned display metadata.
struct DeviceESIMProfile: Codable, Equatable, Sendable {
    let profileID: String
    let iccid: String
    let enabled: Bool
    let nickname: String
    let provider: String

    enum CodingKeys: String, CodingKey {
        case profileID = "profile_id"
        case iccid
        case enabled
        case nickname
        case provider
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
protocol ESIMDeviceProfileTransport: AnyObject {
    func list(line: DeviceLineIdentity) async throws -> [DeviceESIMProfile]
    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> [DeviceESIMProfile]
    func setEnabled(_ enabled: Bool, profileID: String, line: DeviceLineIdentity) async throws
    func rename(_ name: String, profileID: String, line: DeviceLineIdentity) async throws
    func delete(profileID: String, line: DeviceLineIdentity) async throws
}

@MainActor
final class LiveESIMDeviceProfileTransport: ESIMDeviceProfileTransport {
    private let api: any EncryptedCallCommandAPI
    private let commandSigner: InstallationCommandSigner
    private let now: () -> Date
    private let makeCommandID: () -> UUID
    private let makeSenderKey: () -> Curve25519.KeyAgreement.PrivateKey
    private let waitBeforePollingAgain: () async throws -> Void

    init(
        api: any EncryptedCallCommandAPI,
        commandSigner: InstallationCommandSigner,
        now: @escaping () -> Date = Date.init,
        makeCommandID: @escaping () -> UUID = UUID.init,
        makeSenderKey: @escaping () -> Curve25519.KeyAgreement.PrivateKey = { Curve25519.KeyAgreement.PrivateKey() },
        waitBeforePollingAgain: @escaping () async throws -> Void = { try await Task.sleep(for: .milliseconds(500)) }
    ) {
        self.api = api
        self.commandSigner = commandSigner
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.waitBeforePollingAgain = waitBeforePollingAgain
    }

    func list(line: DeviceLineIdentity) async throws -> [DeviceESIMProfile] {
        let result = try await execute(operation: "esim.list", profileID: nil, name: nil, line: line)
        guard result.success, let profiles = result.esimProfiles else {
            throw ESIMProfileManagerError.unavailable
        }
        return profiles
    }

    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> [DeviceESIMProfile] {
        let result = try await execute(
            operation: "esim.download",
            profileID: nil,
            name: nil,
            activationCode: code.trimmingCharacters(in: .whitespacesAndNewlines),
            line: line
        )
        guard result.success, let profiles = result.esimProfiles else {
            throw ESIMProfileManagerError.unavailable
        }
        return profiles
    }

    func setEnabled(_ enabled: Bool, profileID: String, line: DeviceLineIdentity) async throws {
        _ = try await execute(operation: enabled ? "esim.enable" : "esim.disable", profileID: profileID, name: nil, line: line)
    }

    func rename(_ name: String, profileID: String, line: DeviceLineIdentity) async throws {
        _ = try await execute(operation: "esim.rename", profileID: profileID, name: name, line: line)
    }

    func delete(profileID: String, line: DeviceLineIdentity) async throws {
        _ = try await execute(operation: "esim.delete", profileID: profileID, name: nil, line: line)
    }

    private func execute(
        operation: String,
        profileID: String?,
        name: String?,
        activationCode: String? = nil,
        line: DeviceLineIdentity
    ) async throws -> CallCommandResult {
        guard line.isOnline, commandSigner.identity != nil else {
            throw ESIMProfileManagerError.unavailable
        }
        let target = try await api.commandTarget(for: line)
        guard let installationIdentity = commandSigner.identity else {
            throw ESIMProfileManagerError.unavailable
        }
        let commandID = makeCommandID()
        let payload = ESIMCommandPayload(
            version: 1,
            operation: operation,
            slotID: target.slotID,
            simID: target.simID,
            profileID: profileID,
            name: name,
            activationCode: activationCode
        )
        let sealed: SealedCallCommand
        do {
            sealed = try EncryptedCallCommandCodec.seal(
                payload: payload,
                recipientPublicKey: target.publicKey,
                senderPrivateKey: makeSenderKey(),
                nonce: AES.GCM.Nonce(),
                commandID: commandID,
                deviceID: target.deviceID,
                createdAt: Date(timeIntervalSince1970: floor(now().timeIntervalSince1970)),
                installationIdentity: installationIdentity
            )
        } catch {
            throw ESIMProfileManagerError.unavailable
        }
        try await api.enqueueCommand(deviceID: target.deviceID, command: sealed.request)
        for attempt in 0..<60 {
            let status = try await api.commandStatus(deviceID: target.deviceID, commandID: commandID)
            if status.state == "completed" {
                guard let encryptedResult = status.result else { throw ESIMProfileManagerError.unavailable }
                let result: CallCommandResult
                do {
                    result = try EncryptedCallCommandCodec.openResult(
                        encryptedResult,
                        sharedSecret: sealed.sharedSecret,
                        commandID: commandID,
                        deviceID: target.deviceID
                    )
                } catch {
                    throw ESIMProfileManagerError.unavailable
                }
                guard result.success else {
                    if result.errorCode == "esim_unavailable" { throw ESIMProfileManagerError.unsupported }
                    throw ESIMProfileManagerError.unavailable
                }
                return result
            }
            if attempt < 59 { try await waitBeforePollingAgain() }
        }
        throw ESIMProfileManagerError.unavailable
    }
}

private struct ESIMCommandPayload: Codable, Sendable {
    let version: Int
    let operation: String
    let slotID: UUID?
    let simID: UUID?
    let profileID: String?
    let name: String?
    let activationCode: String?

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case slotID = "slot_id"
        case simID = "sim_id"
        case profileID = "esim_profile_id"
        case name = "esim_profile_name"
        case activationCode = "esim_activation_code"
    }
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

/// Persists eSIM profile metadata through the authenticated Owner RPC while
/// the device performs the modem-specific provisioning flow. Activation codes
/// are sealed for the target device and never stored on the server.
@MainActor
final class LiveESIMProfileManager: ESIMProfileManaging {
    private let server: ServerConnectionManager
    private let device: ESIMDeviceProfileTransport

    init(server: ServerConnectionManager, device: ESIMDeviceProfileTransport? = nil) {
        self.server = server
        self.device = device ?? LiveESIMDeviceProfileTransport(
            api: server,
            commandSigner: InstallationCommandSigner()
        )
    }

    func loadProfiles(line: DeviceLineIdentity) async throws -> [ESIMProfile] {
        let simID = try requireSIMID(line)
        let modemProfiles = try await device.list(line: line)
        let metadata = try await server.listESIMProfiles(simID: simID)
        return modemProfiles.map { profile in
            let stored = metadata.first { $0.profileID == profile.profileID }
            return makeProfile(profile, metadata: stored)
        }
    }

    func setEnabled(_ enabled: Bool, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let simID = try requireSIMID(line)
        try await device.setEnabled(enabled, profileID: profile.profileID, line: line)
        _ = try await ensureMetadata(profile, simID: simID)
        let updated = try await server.setESIMProfileEnabled(
            simID: simID, profileID: profile.profileID, enabled: enabled
        )
        return makeProfile(updated, preservingID: profile.id)
    }

    func rename(_ name: String, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let simID = try requireSIMID(line)
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedName.isEmpty else { throw ESIMProfileManagerError.invalidActivationCode }
        try await device.rename(trimmedName, profileID: profile.profileID, line: line)
        _ = try await ensureMetadata(profile, simID: simID)
        let updated = try await server.renameESIMProfile(
            simID: simID, profileID: profile.profileID, name: trimmedName
        )
        return makeProfile(updated, preservingID: profile.id)
    }

    func delete(profile: ESIMProfile, line: DeviceLineIdentity) async throws {
        let simID = try requireSIMID(line)
        try await device.delete(profileID: profile.profileID, line: line)
        try await server.deleteESIMProfile(simID: simID, profileID: profile.profileID)
    }

    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let simID = try requireSIMID(line)
        let trimmedCode = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedCode.isEmpty else {
            throw ESIMProfileManagerError.invalidActivationCode
        }
        let before = try await device.list(line: line)
        let after = try await device.addActivationCode(trimmedCode, line: line)
        let installed = after.first { candidate in
            !before.contains { $0.profileID == candidate.profileID }
        } ?? after.last
        guard let installed else { throw ESIMProfileManagerError.unavailable }
        let metadata = try await server.addESIMProfile(
            simID: simID,
            profileID: installed.profileID,
            name: "",
            iccid: installed.iccid,
            operatorName: installed.provider
        )
        return makeProfile(installed, metadata: metadata)
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

    private func makeProfile(_ profile: DeviceESIMProfile, metadata: Relay_Communication_V1_EsimProfile?) -> ESIMProfile {
        ESIMProfile(
            id: stableID(for: profile.profileID),
            profileID: profile.profileID,
            name: metadata?.name.isEmpty == false ? metadata!.name : profile.nickname,
            iccid: profile.iccid,
            operatorName: metadata?.operatorName.isEmpty == false ? metadata!.operatorName : profile.provider,
            isEnabled: profile.enabled
        )
    }

    private func ensureMetadata(
        _ profile: ESIMProfile,
        simID: UUID
    ) async throws -> Relay_Communication_V1_EsimProfile {
        let existing = try await server.listESIMProfiles(simID: simID).first { $0.profileID == profile.profileID }
        if let existing { return existing }
        return try await server.addESIMProfile(
            simID: simID,
            profileID: profile.profileID,
            name: profile.name,
            iccid: profile.iccid,
            operatorName: profile.operatorName
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

