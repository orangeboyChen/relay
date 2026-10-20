// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import XCTest
@testable import SimDeck

@MainActor
final class SlotSettingsTransportTests: XCTestCase {
    private let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000401")!
    private let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000402")!
    private let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000403")!

    func testLoadQueriesEncryptedSettingsWithoutSendingChanges() async throws {
        let api = try SlotSettingsAPIFake(deviceID: deviceID)
        let transport = makeTransport(api: api)

        let settings = try await transport.load(line: line(commandKey: api.commandPublicKey))

        XCTAssertEqual(settings, api.settings)
        let payload = try XCTUnwrap(api.payloads.first)
        XCTAssertEqual(payload.operation, "slot.configure")
        XCTAssertEqual(payload.slotID, slotID)
        XCTAssertEqual(payload.simID, simID)
        XCTAssertNil(payload.dataEnabled)
        XCTAssertNil(payload.airplaneModeEnabled)
        XCTAssertNil(payload.incomingCallsEnabled)
        XCTAssertNil(payload.incomingSMSEnabled)
        XCTAssertNil(payload.volteEnabled)
        let serverVisible = String(decoding: try JSONEncoder().encode(api.requests[0]), as: UTF8.self)
        XCTAssertFalse(serverVisible.contains("slot.configure"))
        XCTAssertFalse(serverVisible.contains("incoming_sms_enabled"))
    }

    func testUpdateSendsEveryOwnerControlAndReturnsEffectiveSettings() async throws {
        let api = try SlotSettingsAPIFake(deviceID: deviceID)
        let transport = makeTransport(api: api)
        let requested = SlotSettings(
            dataEnabled: false,
            airplaneModeEnabled: true,
            incomingCallsEnabled: false,
            incomingSMSEnabled: false,
            volteEnabled: true
        )

        let effective = try await transport.update(requested, line: line(commandKey: api.commandPublicKey))

        XCTAssertEqual(effective, api.settings)
        let payload = try XCTUnwrap(api.payloads.first)
        XCTAssertEqual(payload.dataEnabled, false)
        XCTAssertEqual(payload.airplaneModeEnabled, true)
        XCTAssertEqual(payload.incomingCallsEnabled, false)
        XCTAssertEqual(payload.incomingSMSEnabled, false)
        XCTAssertEqual(payload.volteEnabled, true)
    }

    func testLoadUsesFreshServerAssignmentInsteadOfStaleScreenIdentifiers() async throws {
        let api = try SlotSettingsAPIFake(deviceID: deviceID)
        let currentSlotID = UUID(uuidString: "01900000-0000-7000-8000-000000000406")!
        let currentSIMID = UUID(uuidString: "01900000-0000-7000-8000-000000000407")!
        api.refreshedTarget = EncryptedCommandTarget(
            deviceID: deviceID,
            slotID: currentSlotID,
            simID: currentSIMID,
            publicKey: api.commandPublicKey
        )

        _ = try await makeTransport(api: api).load(line: line(commandKey: api.commandPublicKey))

        XCTAssertEqual(api.payloads.first?.slotID, currentSlotID)
        XCTAssertEqual(api.payloads.first?.simID, currentSIMID)
    }

    func testOfflineMissingIdentityTimeoutAndInvalidResultFailClosed() async throws {
        let offlineAPI = try SlotSettingsAPIFake(deviceID: deviceID)
        let offlineTransport = makeTransport(api: offlineAPI)
        var offlineLine = line(commandKey: offlineAPI.commandPublicKey)
        offlineLine.isOnline = false
        await assertError(.unavailable) {
            _ = try await offlineTransport.load(line: offlineLine)
        }

        let missingIdentityAPI = try SlotSettingsAPIFake(deviceID: deviceID)
        let missingIdentityTransport = LiveSlotSettingsTransport(
            api: missingIdentityAPI,
            commandSigner: InstallationCommandSigner(),
            waitBeforePollingAgain: {}
        )
        await assertError(.unavailable) {
            _ = try await missingIdentityTransport.load(line: self.line(commandKey: missingIdentityAPI.commandPublicKey))
        }

        let timeoutAPI = try SlotSettingsAPIFake(deviceID: deviceID)
        timeoutAPI.statusState = "pending"
        await assertError(.timedOut) {
            _ = try await self.makeTransport(api: timeoutAPI).load(line: self.line(commandKey: timeoutAPI.commandPublicKey))
        }
        XCTAssertEqual(timeoutAPI.statusRequests, 60)

        let invalidAPI = try SlotSettingsAPIFake(deviceID: deviceID)
        invalidAPI.omitSettings = true
        await assertError(.invalidResponse) {
            _ = try await self.makeTransport(api: invalidAPI).load(line: self.line(commandKey: invalidAPI.commandPublicKey))
        }
    }

    private func makeTransport(api: SlotSettingsAPIFake) -> LiveSlotSettingsTransport {
        LiveSlotSettingsTransport(
            api: api,
            commandSigner: InstallationCommandSigner(identity: api.installationIdentity),
            now: { Date(timeIntervalSince1970: 1_792_138_200) },
            makeCommandID: { UUID(uuidString: "01900000-0000-7000-8000-000000000404")! },
            makeSenderKey: { try! Curve25519.KeyAgreement.PrivateKey(rawRepresentation: Data((33...64).map(UInt8.init))) },
            waitBeforePollingAgain: {}
        )
    }

    private func line(commandKey: Data) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: slotID,
            deviceID: deviceID,
            slotID: slotID,
            simID: simID,
            numericID: 1,
            slotIndex: 1,
            deviceName: "EC20F",
            lineName: "Mobile",
            phoneNumber: "13500000000",
            isOnline: true,
            commandPublicKey: commandKey
        )
    }

    private func assertError(
        _ expected: SlotSettingsTransportError,
        operation: () async throws -> Void
    ) async {
        do {
            try await operation()
            XCTFail("Expected slot settings error")
        } catch {
            XCTAssertEqual(error as? SlotSettingsTransportError, expected)
        }
    }
}

@MainActor
private final class SlotSettingsAPIFake: EncryptedCallCommandAPI {
    let installationIdentity: InstallationIdentity
    let commandPublicKey: Data
    var settings = SlotSettings(
        dataEnabled: true,
        airplaneModeEnabled: false,
        incomingCallsEnabled: true,
        incomingSMSEnabled: false,
        volteEnabled: true
    )
    var statusState = "completed"
    var omitSettings = false
    var refreshedTarget: EncryptedCommandTarget?
    private(set) var payloads: [SlotSettingsCommandPayload] = []
    private(set) var requests: [EncryptedCallCommandRequest] = []
    private(set) var statusRequests = 0

    private let deviceID: UUID
    private let devicePrivateKey: Curve25519.KeyAgreement.PrivateKey
    private var results: [UUID: EncryptedCallCommandResult] = [:]

    init(deviceID: UUID) throws {
        self.deviceID = deviceID
        devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
        commandPublicKey = devicePrivateKey.publicKey.rawRepresentation
        installationIdentity = InstallationIdentity(
            id: UUID(uuidString: "01900000-0000-7000-8000-000000000405")!,
            signingPrivateKey: Data((65...96).map(UInt8.init)),
            keyAgreementPrivateKey: Data((97...128).map(UInt8.init)),
            pendingRevocationIDs: []
        )
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return commandPublicKey
    }

    func commandTarget(for line: DeviceLineIdentity) async throws -> EncryptedCommandTarget {
        XCTAssertEqual(line.deviceID, deviceID)
        return refreshedTarget ?? EncryptedCommandTarget(
            deviceID: deviceID,
            slotID: line.slotID,
            simID: line.simID,
            publicKey: commandPublicKey
        )
    }

    func eventVerificationKey(deviceID: UUID) async throws -> Data { Data(repeating: 0, count: 32) }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        XCTAssertEqual(deviceID, self.deviceID)
        requests.append(command)
        let senderKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: command.senderPublicKey)
        let secret = try devicePrivateKey.sharedSecretFromKeyAgreement(with: senderKey)
        let commandBox = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: command.nonce),
            ciphertext: Data(command.ciphertext.dropLast(16)),
            tag: Data(command.ciphertext.suffix(16))
        )
        let plaintext = try AES.GCM.open(
            commandBox,
            using: Self.key(secret, command.id, deviceID, "sim-deck-command-v1"),
            authenticating: Self.associatedData("sim-deck-command-v1", command.id, deviceID)
        )
        payloads.append(try JSONDecoder().decode(SlotSettingsCommandPayload.self, from: plaintext))
        let result = CallCommandResult(
            version: 1,
            success: true,
            callState: "idle",
            slotSettings: omitSettings ? nil : settings
        )
        let nonce = try AES.GCM.Nonce(data: Data(repeating: 9, count: 12))
        let sealed = try AES.GCM.seal(
            JSONEncoder().encode(result),
            using: Self.key(secret, command.id, deviceID, "sim-deck-command-result-v1"),
            nonce: nonce,
            authenticating: Self.associatedData("sim-deck-command-result-v1", command.id, deviceID)
        )
        results[command.id] = EncryptedCallCommandResult(
            nonce: nonce.withUnsafeBytes { Data($0) },
            ciphertext: sealed.ciphertext + sealed.tag
        )
    }

    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus {
        statusRequests += 1
        return EncryptedCallCommandStatus(
            id: commandID,
            deviceUUID: deviceID,
            state: statusState,
            result: statusState == "completed" ? results[commandID] : nil
        )
    }

    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage {
        EncryptedEventPage(events: [], nextSequence: afterSequence)
    }

    private static func key(
        _ secret: SharedSecret,
        _ commandID: UUID,
        _ deviceID: UUID,
        _ context: String
    ) -> SymmetricKey {
        secret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data(context.utf8),
            outputByteCount: 32
        )
    }

    private static func associatedData(_ context: String, _ commandID: UUID, _ deviceID: UUID) -> Data {
        Data("\(context)\0\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8)
    }
}
