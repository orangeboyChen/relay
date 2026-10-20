// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

struct SlotSettings: Codable, Equatable, Sendable {
    var dataEnabled: Bool
    var airplaneModeEnabled: Bool
    var incomingCallsEnabled: Bool
    var incomingSMSEnabled: Bool
    var volteEnabled: Bool

    init(
        dataEnabled: Bool,
        airplaneModeEnabled: Bool,
        incomingCallsEnabled: Bool,
        incomingSMSEnabled: Bool,
        volteEnabled: Bool = false
    ) {
        self.dataEnabled = dataEnabled
        self.airplaneModeEnabled = airplaneModeEnabled
        self.incomingCallsEnabled = incomingCallsEnabled
        self.incomingSMSEnabled = incomingSMSEnabled
        self.volteEnabled = volteEnabled
    }

    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        dataEnabled = try container.decode(Bool.self, forKey: .dataEnabled)
        airplaneModeEnabled = try container.decode(Bool.self, forKey: .airplaneModeEnabled)
        incomingCallsEnabled = try container.decode(Bool.self, forKey: .incomingCallsEnabled)
        incomingSMSEnabled = try container.decode(Bool.self, forKey: .incomingSMSEnabled)
        volteEnabled = try container.decodeIfPresent(Bool.self, forKey: .volteEnabled) ?? false
    }

    static let enabled = SlotSettings(
        dataEnabled: true,
        airplaneModeEnabled: false,
        incomingCallsEnabled: true,
        incomingSMSEnabled: true,
        volteEnabled: false
    )

    enum CodingKeys: String, CodingKey {
        case dataEnabled = "data_enabled"
        case airplaneModeEnabled = "airplane_mode_enabled"
        case incomingCallsEnabled = "incoming_calls_enabled"
        case incomingSMSEnabled = "incoming_sms_enabled"
        case volteEnabled = "volte_enabled"
    }
}

enum SlotSettingsTransportError: LocalizedError, Equatable {
    case unavailable
    case invalidResponse
    case timedOut
    case rejected

    var errorDescription: String? {
        switch self {
        case .unavailable:
            String(localized: "The slot must be online before its controls can be changed.")
        case .invalidResponse:
            String(localized: "The device returned invalid encrypted slot controls.")
        case .timedOut:
            String(localized: "The device did not respond in time.")
        case .rejected:
            String(localized: "The modem rejected the slot controls.")
        }
    }
}

@MainActor
protocol SlotSettingsTransport: AnyObject {
    func load(line: DeviceLineIdentity) async throws -> SlotSettings
    func update(_ settings: SlotSettings, line: DeviceLineIdentity) async throws -> SlotSettings
}

@MainActor
final class LiveSlotSettingsTransport: SlotSettingsTransport {
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
        makeSenderKey: @escaping () -> Curve25519.KeyAgreement.PrivateKey = {
            Curve25519.KeyAgreement.PrivateKey()
        },
        waitBeforePollingAgain: @escaping () async throws -> Void = {
            try await Task.sleep(for: .milliseconds(500))
        }
    ) {
        self.api = api
        self.commandSigner = commandSigner
        self.now = now
        self.makeCommandID = makeCommandID
        self.makeSenderKey = makeSenderKey
        self.waitBeforePollingAgain = waitBeforePollingAgain
    }

    func load(line: DeviceLineIdentity) async throws -> SlotSettings {
        try await configure(nil, line: line)
    }

    func update(_ settings: SlotSettings, line: DeviceLineIdentity) async throws -> SlotSettings {
        try await configure(settings, line: line)
    }

    private func configure(
        _ settings: SlotSettings?,
        line: DeviceLineIdentity
    ) async throws -> SlotSettings {
        guard line.isOnline, let installationIdentity = commandSigner.identity else {
            throw SlotSettingsTransportError.unavailable
        }
        let target = try await api.commandTarget(for: line)
        let commandID = makeCommandID()
        let payload = SlotSettingsCommandPayload(
            version: 1,
            operation: "slot.configure",
            slotID: target.slotID,
            simID: target.simID,
            dataEnabled: settings?.dataEnabled,
            airplaneModeEnabled: settings?.airplaneModeEnabled,
            incomingCallsEnabled: settings?.incomingCallsEnabled,
            incomingSMSEnabled: settings?.incomingSMSEnabled,
            volteEnabled: settings?.volteEnabled
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
            throw SlotSettingsTransportError.unavailable
        }
        try await api.enqueueCommand(deviceID: target.deviceID, command: sealed.request)
        for attempt in 0..<60 {
            let status = try await api.commandStatus(deviceID: target.deviceID, commandID: commandID)
            if status.state == "completed" {
                guard let encryptedResult = status.result else {
                    throw SlotSettingsTransportError.invalidResponse
                }
                let result: CallCommandResult
                do {
                    result = try EncryptedCallCommandCodec.openResult(
                        encryptedResult,
                        sharedSecret: sealed.sharedSecret,
                        commandID: commandID,
                        deviceID: target.deviceID
                    )
                } catch {
                    throw SlotSettingsTransportError.invalidResponse
                }
                guard result.success else { throw SlotSettingsTransportError.rejected }
                guard let slotSettings = result.slotSettings else {
                    throw SlotSettingsTransportError.invalidResponse
                }
                return slotSettings
            }
            if attempt < 59 {
                try await waitBeforePollingAgain()
            }
        }
        throw SlotSettingsTransportError.timedOut
    }
}

struct SlotSettingsCommandPayload: Codable, Equatable, Sendable {
    let version: Int
    let operation: String
    let slotID: UUID?
    let simID: UUID?
    let dataEnabled: Bool?
    let airplaneModeEnabled: Bool?
    let incomingCallsEnabled: Bool?
    let incomingSMSEnabled: Bool?
    let volteEnabled: Bool?

    enum CodingKeys: String, CodingKey {
        case version
        case operation
        case slotID = "slot_id"
        case simID = "sim_id"
        case dataEnabled = "data_enabled"
        case airplaneModeEnabled = "airplane_mode_enabled"
        case incomingCallsEnabled = "incoming_calls_enabled"
        case incomingSMSEnabled = "incoming_sms_enabled"
        case volteEnabled = "volte_enabled"
    }
}
