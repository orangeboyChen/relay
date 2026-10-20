// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

final class AppModelTests: XCTestCase {
    func testLineDisplayNamePrefersPhoneNumber() {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Office EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true
        )

        XCTAssertEqual(line.displayName, "13500000000")
    }

    func testLineDisplayNameFallsBackToNumericID() {
        let line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Office EC20",
            lineName: "",
            phoneNumber: "",
            isOnline: true,
            commandPublicKey: nil
        )

        XCTAssertEqual(line.displayName, "SIM 2")
    }

    func testCallDurationUsesRecordedDates() {
        let start = Date(timeIntervalSince1970: 1_000)
        let call = CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "10086",
            contactName: "Carrier",
            direction: .incoming,
            result: .answered,
            startedAt: start,
            endedAt: start.addingTimeInterval(119)
        )

        XCTAssertEqual(call.displayName, "Carrier")
        XCTAssertEqual(call.duration, 119)
    }

    func testDialStringNormalizationAndValidation() {
        XCTAssertEqual(DialString.normalized("+86 135 0000 0000"), "+8613500000000")
        XCTAssertTrue(DialString.isValid("+86 135 0000 0000"))
        XCTAssertTrue(DialString.isValid("*100#"))
        XCTAssertFalse(DialString.isValid("135+1041"))
        XCTAssertFalse(DialString.isValid("1"))
    }

    func testConversationIdentitySeparatesLines() {
        let remoteNumber = "13800138000"
        let first = ConversationID(deviceUUID: UUID(), remoteNumber: remoteNumber)
        let second = ConversationID(deviceUUID: UUID(), remoteNumber: remoteNumber)

        XCTAssertNotEqual(first, second)
    }

    func testPreviewFixturesKeepInsertedSIMAssignmentsConsistent() throws {
        for line in PreviewFixtures.devices {
            let simID = try XCTUnwrap(line.simID)
            let sim = try XCTUnwrap(PreviewFixtures.managedSIMs.first { $0.id == simID })

            XCTAssertEqual(sim.assignedDeviceID, line.deviceID)
            XCTAssertEqual(sim.assignedSlotID, line.slotID)
            XCTAssertNotNil(line.slotIndex)
        }
    }

    @MainActor
    func testDisabledRemoteRefreshPreservesInjectedDevices() async {
        let fixture = Self.line(id: UUID(), numericID: 1)
        let model = AppModel(
            devices: [fixture],
            remoteDeviceRefreshEnabled: false
        )

        await model.refreshDevices()

        XCTAssertEqual(model.devices, [fixture])
    }

    @MainActor
    func testRefreshDevicesPublishesServerDeviceLines() async throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
        let session = Data("""
        {"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}
        """.utf8)
        let network = AppModelServerNetwork(deviceID: deviceID)
        let defaults = UserDefaults(suiteName: "AppModelTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: network,
            sessionStore: AppModelSessionStore(value: session),
            defaults: defaults
        )
        let model = AppModel(serverConnection: connection)

        await model.refreshDevices()

        XCTAssertEqual(model.devices.map(\.id), [deviceID])
        XCTAssertEqual(model.devices.map(\.numericID), [3])
        XCTAssertEqual(model.devices.map(\.commandPublicKey), [Data(repeating: 4, count: 32)])
        XCTAssertEqual(network.requestCount, 1)
    }

    @MainActor
    func testServerSignInLoadsDevicesAndSignOutClearsThem() async throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000002")!
        let network = AppModelSignInNetwork(deviceID: deviceID)
        let sessionStore = AppModelSessionStore(value: nil)
        let defaults = UserDefaults(suiteName: "AppModelSignInTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: network,
            sessionStore: sessionStore,
            defaults: defaults
        )
        let model = AppModel(serverConnection: connection)

        try await model.signInToServer(password: "bootstrap secret")

        XCTAssertEqual(connection.state, .connected)
        XCTAssertEqual(model.devices.map(\.id), [deviceID])
        XCTAssertNotNil(sessionStore.storedValue)

        try await model.signOutFromServer()

        XCTAssertEqual(connection.state, .disconnected)
        XCTAssertTrue(model.devices.isEmpty)
        XCTAssertNil(sessionStore.storedValue)
    }

    @MainActor
    func testRefreshRegistersBootstrappedInstallationBeforeLoadingDevices() async throws {
        let session = Data("""
        {"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}
        """.utf8)
        let identity = InstallationIdentity.generate()
        let network = AppModelInstallationNetwork(identity: identity)
        let defaults = UserDefaults(suiteName: "AppModelInstallationTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: network,
            sessionStore: AppModelSessionStore(value: session),
            defaults: defaults
        )
        let model = AppModel(
            serverConnection: connection,
            installationBootstrapper: AppModelInstallationBootstrapper(identity: identity)
        )

        model.bootstrapInstallation()
        await model.refreshDevices()

        XCTAssertEqual(network.requestPaths, ["/v1/app-installations", "/v1/devices"])
        XCTAssertEqual(model.installationID, identity.id)
        XCTAssertTrue(model.installationTrust?.isApproved == true)
    }

    @MainActor
    func testCorruptMessageHistoryDoesNotBlockCallHistoryOrKeyRecovery() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let messageURL = directory.appending(path: "messages.bin")
        try Data("not encrypted history".utf8).write(to: messageURL)
        let messageStore = EncryptedMessageHistoryStore(fileURL: messageURL)
        let callStore = EncryptedCallHistoryStore(fileURL: directory.appending(path: "calls.bin"))
        let vaultKey = VaultKey.random()
        let record = Self.callRecord()
        try callStore.save([record], using: vaultKey)
        let model = AppModel(
            installationBootstrapper: AppModelInstallationBootstrapper(
                identity: .generate(),
                vaultKey: vaultKey
            ),
            messageHistoryStore: messageStore,
            callHistoryStore: callStore,
            remoteDeviceRefreshEnabled: false
        )

        model.bootstrapInstallation()

        XCTAssertEqual(model.keyRecoveryState, .available)
        XCTAssertEqual(model.messages, .failed)
        XCTAssertEqual(model.callHistory, .loaded([record]))
    }

    @MainActor
    func testCorruptCallHistoryDoesNotBlockMessageHistoryOrKeyRecovery() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let callURL = directory.appending(path: "calls.bin")
        try Data("not encrypted history".utf8).write(to: callURL)
        let callStore = EncryptedCallHistoryStore(fileURL: callURL)
        let messageStore = EncryptedMessageHistoryStore(
            fileURL: directory.appending(path: "messages.bin")
        )
        let vaultKey = VaultKey.random()
        let conversation = MessageConversation(
            lineID: UUID(),
            remoteNumber: "13500000000",
            contactName: nil,
            unreadCount: 0,
            messages: []
        )
        try messageStore.save([conversation], using: vaultKey)
        let model = AppModel(
            installationBootstrapper: AppModelInstallationBootstrapper(
                identity: .generate(),
                vaultKey: vaultKey
            ),
            messageHistoryStore: messageStore,
            callHistoryStore: callStore,
            remoteDeviceRefreshEnabled: false
        )

        model.bootstrapInstallation()

        XCTAssertEqual(model.keyRecoveryState, .available)
        XCTAssertEqual(model.messages, .loaded([conversation]))
        XCTAssertEqual(model.callHistory, .failed)
    }

    @MainActor
    func testRevokingCurrentSessionDeletesEncryptedLocalHistory() async throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let messageURL = directory.appending(path: "messages.bin")
        let callURL = directory.appending(path: "calls.bin")
        let messageStore = EncryptedMessageHistoryStore(fileURL: messageURL)
        let callStore = EncryptedCallHistoryStore(fileURL: callURL)
        let vaultKey = VaultKey.random()
        try messageStore.save([], using: vaultKey)
        try callStore.save([Self.callRecord()], using: vaultKey)
        let defaults = UserDefaults(suiteName: "AppModelRevokeTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: AppModelLogoutNetwork(),
            sessionStore: AppModelSessionStore(value: Self.activeSession),
            defaults: defaults
        )
        let model = AppModel(
            callHistory: .loaded([Self.callRecord()]),
            messages: .loaded([]),
            serverConnection: connection,
            messageHistoryStore: messageStore,
            callHistoryStore: callStore,
            remoteDeviceRefreshEnabled: false
        )

        try await model.revokeCurrentSession()

        XCTAssertEqual(model.messages, .loaded([]))
        XCTAssertEqual(model.callHistory, .loaded([]))
        XCTAssertFalse(FileManager.default.fileExists(atPath: messageURL.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: callURL.path))
        XCTAssertFalse(connection.isSignedIn)
    }

    @MainActor
    func testRevokingCurrentSessionUnregistersPushTokensAndClearsNotificationKeys() async throws {
        let identity = InstallationIdentity.generate()
        let network = AppModelPushLogoutNetwork()
        let defaults = UserDefaults(suiteName: "AppModelPushLogoutTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let sharedDefaults = NotificationPreferences.sharedDefaults
        sharedDefaults.set(Data(repeating: 9, count: 32), forKey: NotificationSharedState.deviceKeysKey)
        defer { sharedDefaults.removeObject(forKey: NotificationSharedState.deviceKeysKey) }
        let connection = ServerConnectionManager(
            network: network,
            sessionStore: AppModelSessionStore(value: Self.activeSession),
            defaults: defaults
        )
        let model = AppModel(
            serverConnection: connection,
            installationBootstrapper: AppModelInstallationBootstrapper(identity: identity),
            remoteDeviceRefreshEnabled: false
        )
        model.bootstrapInstallation()

        try await model.revokeCurrentSession()

        XCTAssertEqual(
            network.requests.map { ($0.httpMethod ?? "") + " " + ($0.url?.path ?? "") },
            [
                "DELETE /v1/app-installations/\(identity.id.uuidString.lowercased())/push-tokens/voip",
                "DELETE /v1/app-installations/\(identity.id.uuidString.lowercased())/push-tokens/alert",
                "POST /v1/auth/logout",
            ]
        )
        XCTAssertNil(sharedDefaults.data(forKey: NotificationSharedState.deviceKeysKey))
        XCTAssertFalse(connection.isSignedIn)
    }

    @MainActor
    func testSavesEncryptedDeviceMetadataAndReloadsProfile() async throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000008")!
        let identity = InstallationIdentity.generate()
        let vaultKey = try VaultKey(data: Data(repeating: 3, count: VaultKey.length))
        let network = AppModelDeviceManagementNetwork(deviceID: deviceID)
        let defaults = UserDefaults(suiteName: "AppModelDeviceSaveTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: network,
            sessionStore: AppModelSessionStore(value: Self.activeSession),
            defaults: defaults
        )
        let model = AppModel(
            devices: [Self.line(id: deviceID, numericID: 1)],
            serverConnection: connection,
            installationBootstrapper: AppModelInstallationBootstrapper(
                identity: identity,
                vaultKey: vaultKey
            )
        )
        model.bootstrapInstallation()

        try await model.saveDevice(
            line: try XCTUnwrap(model.devices.first),
            numericID: 7,
            deviceName: "  Living Room EC20  ",
            lineName: "  Personal  ",
            phoneNumber: "  13500000000  "
        )

        let ciphertext = try XCTUnwrap(network.profileCiphertext)
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("Living Room"))
        XCTAssertEqual(
            try DeviceProfileCodec.open(ciphertext, using: vaultKey),
            DeviceProfile(
                deviceName: "Living Room EC20",
                lineName: "Personal",
                phoneNumber: "13500000000"
            )
        )
        XCTAssertEqual(model.devices.first?.numericID, 7)
        XCTAssertEqual(model.devices.first?.phoneNumber, "13500000000")
        XCTAssertEqual(network.requestMethods, ["PUT", "PATCH", "GET"])
    }

    @MainActor
    func testSavingDeviceWithoutRecoveredVaultKeyFailsBeforeNetworkRequest() async {
        let deviceID = UUID()
        let network = AppModelDeviceManagementNetwork(deviceID: deviceID)
        let defaults = UserDefaults(suiteName: "AppModelDeviceNoKeyTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let model = AppModel(
            devices: [Self.line(id: deviceID, numericID: 1)],
            serverConnection: ServerConnectionManager(
                network: network,
                sessionStore: AppModelSessionStore(value: Self.activeSession),
                defaults: defaults
            )
        )

        do {
            try await model.saveDevice(
                line: try XCTUnwrap(model.devices.first),
                numericID: 2,
                deviceName: "Office",
                lineName: "Work",
                phoneNumber: "10086"
            )
            XCTFail("Expected missing recovery key to fail")
        } catch {
            XCTAssertEqual(error as? KeyRecoveryError, .invalidVaultKey)
        }

        XCTAssertTrue(network.requestMethods.isEmpty)
    }

    @MainActor
    func testRemovingDeviceClearsLineDefaults() async throws {
        let deviceID = UUID()
        let firstSIMID = UUID()
        let secondSIMID = UUID()
        let otherDeviceID = UUID()
        let network = AppModelDeviceManagementNetwork(deviceID: deviceID)
        let defaults = UserDefaults(suiteName: "AppModelDeviceRemoveTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let model = AppModel(
            devices: [
                Self.line(id: firstSIMID, deviceID: deviceID, simID: firstSIMID, numericID: 1),
                Self.line(id: secondSIMID, deviceID: deviceID, simID: secondSIMID, numericID: 2),
                Self.line(id: otherDeviceID, numericID: 3)
            ],
            lastSuccessfulDialLineID: firstSIMID,
            defaultCallLineID: firstSIMID,
            defaultMessageLineID: secondSIMID,
            serverConnection: ServerConnectionManager(
                network: network,
                sessionStore: AppModelSessionStore(value: Self.activeSession),
                defaults: defaults
            )
        )

        try await model.removeDevice(line: model.devices[0])

        XCTAssertEqual(model.devices.map(\.id), [otherDeviceID])
        XCTAssertNil(model.defaultCallLineID)
        XCTAssertNil(model.defaultMessageLineID)
        XCTAssertNil(model.lastSuccessfulDialLineID)
        XCTAssertEqual(network.requestMethods, ["DELETE"])
    }

    @MainActor
    func testSavesSlotLineThroughSIMEndpoints() async throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000021")!
        let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000022")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000023")!
        let identity = InstallationIdentity.generate()
        let vaultKey = try VaultKey(data: Data(repeating: 4, count: VaultKey.length))
        let network = AppModelDeviceManagementNetwork(
            deviceID: deviceID,
            slotID: slotID,
            simID: simID
        )
        let defaults = UserDefaults(suiteName: "AppModelSIMSaveTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let line = Self.line(
            id: simID,
            deviceID: deviceID,
            slotID: slotID,
            simID: simID,
            numericID: 2
        )
        let model = AppModel(
            devices: [line],
            serverConnection: ServerConnectionManager(
                network: network,
                sessionStore: AppModelSessionStore(value: Self.activeSession),
                defaults: defaults
            ),
            installationBootstrapper: AppModelInstallationBootstrapper(
                identity: identity,
                vaultKey: vaultKey
            )
        )
        model.bootstrapInstallation()

        try await model.saveDevice(
            line: line,
            numericID: 9,
            deviceName: "Desk Modem",
            lineName: "Work",
            phoneNumber: "13800000000"
        )

        XCTAssertEqual(
            network.requestPaths,
            [
                "/v1/devices/\(deviceID.uuidString.lowercased())/profile",
                "/v1/sims/\(simID.uuidString.lowercased())/profile",
                "/v1/sims/\(simID.uuidString.lowercased())/numeric-id",
                "/v1/devices"
            ]
        )
        XCTAssertEqual(model.devices.first?.id, slotID)
        XCTAssertEqual(model.devices.first?.deviceID, deviceID)
        XCTAssertEqual(model.devices.first?.slotID, slotID)
        XCTAssertEqual(model.devices.first?.numericID, 9)
    }

    private static let activeSession = Data("""
    {"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}
    """.utf8)

    private static func line(
        id: UUID,
        deviceID: UUID? = nil,
        slotID: UUID? = nil,
        simID: UUID? = nil,
        numericID: Int
    ) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: id,
            deviceID: deviceID,
            slotID: slotID,
            simID: simID,
            numericID: numericID,
            deviceName: "",
            lineName: "",
            phoneNumber: "",
            isOnline: true,
            commandPublicKey: nil
        )
    }

    private static func callRecord() -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: UUID(),
            remoteNumber: "13800000000",
            contactName: nil,
            direction: .incoming,
            result: .missed,
            startedAt: .now,
            endedAt: .now
        )
    }
}

@MainActor
private final class AppModelServerNetwork: ServerNetworking {
    private let deviceID: UUID
    private(set) var requestCount = 0

    init(deviceID: UUID) {
        self.deviceID = deviceID
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requestCount += 1
        XCTAssertEqual(request.url?.path, "/v1/devices")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
        let key = Data(repeating: 4, count: 32).base64EncodedString()
        let body = Data("""
        {"devices":[{"device_uuid":"\(deviceID.uuidString.lowercased())","numeric_id":3,"profile_ciphertext":"","identity_public_key":"","command_public_key":"\(key)","paired_at":"2026-10-16T00:00:00Z"}]}
        """.utf8)
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: 200,
            httpVersion: nil,
            headerFields: nil
        )!
        return (body, response)
    }
}

private final class AppModelLogoutNetwork: ServerNetworking {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/v1/auth/logout")
        return (
            Data(),
            HTTPURLResponse(
                url: request.url!,
                statusCode: 204,
                httpVersion: nil,
                headerFields: nil
            )!
        )
    }
}

private final class AppModelPushLogoutNetwork: ServerNetworking, @unchecked Sendable {
    private let lock = NSLock()
    private var storedRequests: [URLRequest] = []

    var requests: [URLRequest] { lock.withLock { storedRequests } }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        lock.withLock { storedRequests.append(request) }
        return (
            Data(),
            HTTPURLResponse(
                url: request.url!,
                statusCode: 204,
                httpVersion: nil,
                headerFields: nil
            )!
        )
    }
}

private final class AppModelSessionStore: SecureValueStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var value: Data?

    init(value: Data?) {
        self.value = value
    }

    var storedValue: Data? { lock.withLock { value } }

    func read() -> Data? { lock.withLock { value } }
    func write(_ value: Data) { lock.withLock { self.value = value } }
    func delete() { lock.withLock { value = nil } }
}

private struct AppModelInstallationBootstrapper: InstallationBootstrapping {
    let identity: InstallationIdentity
    var vaultKey: VaultKey? = try? VaultKey(
        data: Data(repeating: 3, count: VaultKey.length)
    )

    func bootstrap() throws -> InstallationBootstrapResult {
        InstallationBootstrapResult(
            identity: identity,
            recoveredVaultKey: vaultKey,
            pendingRevocationIDs: []
        )
    }

    func saveVaultKey(_: VaultKey) throws {}
    func markInstallationRevoked(_: UUID) throws {}
}

@MainActor
private final class AppModelDeviceManagementNetwork: ServerNetworking {
    let deviceID: UUID
    private(set) var requestMethods: [String] = []
    private(set) var requestPaths: [String] = []
    private(set) var profileCiphertext: Data?
    private var deviceProfileCiphertext: Data?
    private var numericID = 1
    private let slotID: UUID?
    private let simID: UUID?

    init(deviceID: UUID, slotID: UUID? = nil, simID: UUID? = nil) {
        self.deviceID = deviceID
        self.slotID = slotID
        self.simID = simID
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requestMethods.append(request.httpMethod ?? "")
        requestPaths.append(request.url?.path ?? "")
        let deviceBasePath = "/v1/devices/\(deviceID.uuidString.lowercased())"
        let lineBasePath = simID.map { "/v1/sims/\($0.uuidString.lowercased())" } ?? deviceBasePath
        let body: Data
        let status: Int
        switch (request.httpMethod, request.url?.path) {
        case ("PUT", "\(deviceBasePath)/profile") where simID != nil:
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            deviceProfileCiphertext = Data(base64Encoded: try XCTUnwrap(object["profile_ciphertext"] as? String))
            body = Data()
            status = 204
        case ("PUT", "\(lineBasePath)/profile"):
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            profileCiphertext = Data(
                base64Encoded: try XCTUnwrap(object["profile_ciphertext"] as? String)
            )
            body = Data()
            status = 204
        case ("PATCH", "\(lineBasePath)/numeric-id"):
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            numericID = try XCTUnwrap(object["numeric_id"] as? Int)
            body = Data()
            status = 204
        case ("GET", "/v1/devices"):
            let slots = if let slotID, let simID {
                """
                ,"slots":[{
                  "slot_id":"\(slotID.uuidString.lowercased())",
                  "slot_key":"1",
                  "slot_index":1,
                  "sim_id":"\(simID.uuidString.lowercased())",
                  "numeric_id":\(numericID),
                  "profile_ciphertext":"\((profileCiphertext ?? Data()).base64EncodedString())"
                }]
                """
            } else {
                ""
            }
            body = Data("""
                {"devices":[{
                  "device_uuid":"\(deviceID.uuidString.lowercased())",
                  "numeric_id":\(numericID),
                  "profile_ciphertext":"\((deviceProfileCiphertext ?? profileCiphertext ?? Data()).base64EncodedString())",
                  "identity_public_key":"",
                  "paired_at":"2026-10-16T00:00:00Z"\(slots)
                }]}
                """.utf8)
            status = 200
        case ("DELETE", deviceBasePath):
            body = Data()
            status = 204
        default:
            XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.path ?? "")")
            body = Data("{}".utf8)
            status = 500
        }
        return (
            body,
            HTTPURLResponse(
                url: request.url!,
                statusCode: status,
                httpVersion: nil,
                headerFields: nil
            )!
        )
    }
}

@MainActor
private final class AppModelInstallationNetwork: ServerNetworking {
    let identity: InstallationIdentity
    private(set) var requestPaths: [String] = []

    init(identity: InstallationIdentity) {
        self.identity = identity
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        requestPaths.append(path)
        let body: Data
        let status: Int
        switch (request.httpMethod, path) {
        case ("POST", "/v1/app-installations"):
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            XCTAssertEqual(
                (object["installation_id"] as? String).flatMap(UUID.init(uuidString:)),
                identity.id
            )
            XCTAssertEqual(
                object["identity_public_key"] as? String,
                try identity.signingPublicKey().base64EncodedString()
            )
            XCTAssertEqual(
                object["key_agreement_public_key"] as? String,
                try identity.keyAgreementPublicKey().base64EncodedString()
            )
            body = Data("""
            {"installation":{
              "installation_id":"\(identity.id.uuidString.lowercased())",
              "identity_public_key":"\(try identity.signingPublicKey().base64EncodedString())",
              "key_agreement_public_key":"\(try identity.keyAgreementPublicKey().base64EncodedString())",
              "approved_at":"2026-10-16T13:00:00Z",
              "created_at":"2026-10-16T13:00:00Z"
            },"created":true}
            """.utf8)
            status = 201
        case ("GET", "/v1/devices"):
            body = Data("{\"devices\":[]}".utf8)
            status = 200
        default:
            XCTFail("Unexpected request: \(request.httpMethod ?? "") \(path)")
            body = Data("{}".utf8)
            status = 500
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: nil
        )!
        return (body, response)
    }
}

@MainActor
private final class AppModelSignInNetwork: ServerNetworking {
    private let deviceID: UUID

    init(deviceID: UUID) {
        self.deviceID = deviceID
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let body: Data
        let status: Int
        switch (request.httpMethod, request.url?.path) {
        case ("POST", "/v1/auth/bootstrap"):
            XCTAssertTrue(String(decoding: request.httpBody ?? Data(), as: UTF8.self).contains("bootstrap secret"))
            body = Data("""
            {"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}
            """.utf8)
            status = 201
        case ("GET", "/v1/devices"):
            let key = Data(repeating: 5, count: 32).base64EncodedString()
            body = Data("""
            {"devices":[{"device_uuid":"\(deviceID.uuidString.lowercased())","numeric_id":5,"profile_ciphertext":"","identity_public_key":"","command_public_key":"\(key)","paired_at":"2026-10-16T00:00:00Z"}]}
            """.utf8)
            status = 200
        default:
            XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.path ?? "")")
            body = Data("{}".utf8)
            status = 500
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: nil
        )!
        return (body, response)
    }
}
