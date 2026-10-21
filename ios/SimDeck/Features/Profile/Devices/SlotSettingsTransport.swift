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
    var mmsAPN: String
    var mmscURL: String
    var mmsProxyHost: String
    var mmsProxyPort: Int
    var mmsContextID: Int

    init(
        dataEnabled: Bool,
        airplaneModeEnabled: Bool,
        incomingCallsEnabled: Bool,
        incomingSMSEnabled: Bool,
        volteEnabled: Bool = false,
        mmsAPN: String = "",
        mmscURL: String = "",
        mmsProxyHost: String = "",
        mmsProxyPort: Int = 80,
        mmsContextID: Int = 1
    ) {
        self.dataEnabled = dataEnabled
        self.airplaneModeEnabled = airplaneModeEnabled
        self.incomingCallsEnabled = incomingCallsEnabled
        self.incomingSMSEnabled = incomingSMSEnabled
        self.volteEnabled = volteEnabled
        self.mmsAPN = mmsAPN
        self.mmscURL = mmscURL
        self.mmsProxyHost = mmsProxyHost
        self.mmsProxyPort = mmsProxyPort
        self.mmsContextID = mmsContextID
    }

    init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        dataEnabled = try container.decode(Bool.self, forKey: .dataEnabled)
        airplaneModeEnabled = try container.decode(Bool.self, forKey: .airplaneModeEnabled)
        incomingCallsEnabled = try container.decode(Bool.self, forKey: .incomingCallsEnabled)
        incomingSMSEnabled = try container.decode(Bool.self, forKey: .incomingSMSEnabled)
        volteEnabled = try container.decodeIfPresent(Bool.self, forKey: .volteEnabled) ?? false
        mmsAPN = try container.decodeIfPresent(String.self, forKey: .mmsAPN) ?? ""
        mmscURL = try container.decodeIfPresent(String.self, forKey: .mmscURL) ?? ""
        mmsProxyHost = try container.decodeIfPresent(String.self, forKey: .mmsProxyHost) ?? ""
        mmsProxyPort = try container.decodeIfPresent(Int.self, forKey: .mmsProxyPort) ?? 80
        mmsContextID = try container.decodeIfPresent(Int.self, forKey: .mmsContextID) ?? 1
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
        case mmsAPN = "mms_apn"
        case mmscURL = "mmsc_url"
        case mmsProxyHost = "mms_proxy_host"
        case mmsProxyPort = "mms_proxy_port"
        case mmsContextID = "mms_context_id"
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
    func restartModem(line: DeviceLineIdentity) async throws
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

    func restartModem(line: DeviceLineIdentity) async throws {
        guard line.isOnline, commandSigner.identity != nil else {
            throw SlotSettingsTransportError.unavailable
        }
        let target = try await api.commandTarget(for: line)
        let result = try await execute(
            SlotSettingsCommandPayload(
                version: 1,
                operation: "slot.restart_modem",
                slotID: target.slotID,
                simID: target.simID,
                dataEnabled: nil,
                airplaneModeEnabled: nil,
                incomingCallsEnabled: nil,
                incomingSMSEnabled: nil,
                volteEnabled: nil,
                mmsAPN: nil,
                mmscURL: nil,
                mmsProxyHost: nil,
                mmsProxyPort: nil,
                mmsContextID: nil
            ),
            target: target,
            line: line
        )
        guard result.success else { throw SlotSettingsTransportError.rejected }
    }

    private func configure(
        _ settings: SlotSettings?,
        line: DeviceLineIdentity
    ) async throws -> SlotSettings {
        guard line.isOnline, commandSigner.identity != nil else {
            throw SlotSettingsTransportError.unavailable
        }
        let target = try await api.commandTarget(for: line)
        let payload = SlotSettingsCommandPayload(
            version: 1,
            operation: "slot.configure",
            slotID: target.slotID,
            simID: target.simID,
            dataEnabled: settings?.dataEnabled,
            airplaneModeEnabled: settings?.airplaneModeEnabled,
            incomingCallsEnabled: settings?.incomingCallsEnabled,
            incomingSMSEnabled: settings?.incomingSMSEnabled,
            volteEnabled: settings?.volteEnabled,
            mmsAPN: settings?.mmsAPN,
            mmscURL: settings?.mmscURL,
            mmsProxyHost: settings?.mmsProxyHost,
            mmsProxyPort: settings?.mmsProxyPort,
            mmsContextID: settings?.mmsContextID
        )
        let result = try await execute(payload, target: target, line: line)
        guard result.success else { throw SlotSettingsTransportError.rejected }
        guard let slotSettings = result.slotSettings else {
            throw SlotSettingsTransportError.invalidResponse
        }
        return slotSettings
    }

    private func execute(
        _ payload: SlotSettingsCommandPayload,
        target: EncryptedCommandTarget,
        line: DeviceLineIdentity
    ) async throws -> CallCommandResult {
        guard line.isOnline, let installationIdentity = commandSigner.identity else {
            throw SlotSettingsTransportError.unavailable
        }
        let commandID = makeCommandID()
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
                return result
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
    let mmsAPN: String?
    let mmscURL: String?
    let mmsProxyHost: String?
    let mmsProxyPort: Int?
    let mmsContextID: Int?

    init(
        version: Int,
        operation: String,
        slotID: UUID?,
        simID: UUID?,
        dataEnabled: Bool?,
        airplaneModeEnabled: Bool?,
        incomingCallsEnabled: Bool?,
        incomingSMSEnabled: Bool?,
        volteEnabled: Bool?,
        mmsAPN: String? = nil,
        mmscURL: String? = nil,
        mmsProxyHost: String? = nil,
        mmsProxyPort: Int? = nil,
        mmsContextID: Int? = nil
    ) {
        self.version = version
        self.operation = operation
        self.slotID = slotID
        self.simID = simID
        self.dataEnabled = dataEnabled
        self.airplaneModeEnabled = airplaneModeEnabled
        self.incomingCallsEnabled = incomingCallsEnabled
        self.incomingSMSEnabled = incomingSMSEnabled
        self.volteEnabled = volteEnabled
        self.mmsAPN = mmsAPN
        self.mmscURL = mmscURL
        self.mmsProxyHost = mmsProxyHost
        self.mmsProxyPort = mmsProxyPort
        self.mmsContextID = mmsContextID
    }

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
        case mmsAPN = "mms_apn"
        case mmscURL = "mmsc_url"
        case mmsProxyHost = "mms_proxy_host"
        case mmsProxyPort = "mms_proxy_port"
        case mmsContextID = "mms_context_id"
    }
}
