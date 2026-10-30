// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
import Connect
import RelayProtocol
import SwiftProtobuf
@testable import SimDeck

@MainActor
private final class FakePasskeyRPCClient: PasskeyRPCClient {
    var bootstrapSession = ServerSession(
        accessToken: "bootstrap-access", refreshToken: "bootstrap-refresh",
        accessExpiresAt: Date(timeIntervalSince1970: 1_900_000_000),
        refreshExpiresAt: Date(timeIntervalSince1970: 2_000_000_000)
    )
    var refreshedSession = ServerSession(
        accessToken: "fresh-access", refreshToken: "refresh-two",
        accessExpiresAt: Date(timeIntervalSince1970: 1_900_000_000),
        refreshExpiresAt: Date(timeIntervalSince1970: 2_000_000_000)
    )
    var bootstrapUsername = ""
    var bootstrapPassword = ""
    var refreshToken = ""
    var logoutAccessToken = ""
    var refreshHandler: ((String) async throws -> ServerSession)?
    var registrationOptions = PasskeyOptionsPayload(ceremonyID: "registration-1", optionsJSON: Data())
    var loginOptions = PasskeyOptionsPayload(ceremonyID: "login-1", optionsJSON: Data())
    var loginSession = ServerSession(
        accessToken: "passkey-access",
        refreshToken: "passkey-refresh",
        accessExpiresAt: Date(timeIntervalSince1970: 1_900_000_000),
        refreshExpiresAt: Date(timeIntervalSince1970: 2_000_000_000)
    )
    var registeredCeremonyID = ""
    var registeredCredentialJSON = Data()
    var registrationAccessToken = ""
    var loginCeremonyID = ""
    var loginCredentialJSON = Data()

    func bootstrap(username: String, password: String) async throws -> ServerSession {
        bootstrapUsername = username
        bootstrapPassword = password
        return bootstrapSession
    }

    func refresh(refreshToken: String) async throws -> ServerSession {
        self.refreshToken = refreshToken
        if let refreshHandler { return try await refreshHandler(refreshToken) }
        return refreshedSession
    }

    func logout(accessToken: String) async throws { logoutAccessToken = accessToken }

    func beginRegistration(accessToken: String) async throws -> PasskeyOptionsPayload {
        registrationAccessToken = accessToken
        return registrationOptions
    }

    func finishRegistration(ceremonyID: String, credentialJSON: Data, accessToken: String) async throws {
        registeredCeremonyID = ceremonyID
        registeredCredentialJSON = credentialJSON
        registrationAccessToken = accessToken
    }

    func beginLogin() async throws -> PasskeyOptionsPayload { loginOptions }

    func finishLogin(ceremonyID: String, credentialJSON: Data) async throws -> ServerSession {
        loginCeremonyID = ceremonyID
        loginCredentialJSON = credentialJSON
        return loginSession
    }
}

@MainActor
private final class FakePasskeyRPCClientFactory: PasskeyRPCClientCreating {
    let client: FakePasskeyRPCClient

    init(client: FakePasskeyRPCClient) { self.client = client }

    func makeClient(baseURL: URL) -> any PasskeyRPCClient {
        XCTAssertEqual(baseURL.host, "deck.example.test")
        return client
    }
}

@MainActor
final class ServerConnectionManagerTests: XCTestCase {
    func testWebPasskeyAuthorizationURLUsesConfiguredServerAddress() throws {
        let url = try WebPasskeyAuthorizer.authorizationURL(
            baseURL: URL(string: "https://deck.example.test:54443")!,
            path: "passkey/login",
            parameters: [:],
            state: "test-state"
        )

        XCTAssertEqual(url.scheme, "https")
        XCTAssertEqual(url.host, "deck.example.test")
        XCTAssertEqual(url.port, 54443)
        XCTAssertEqual(url.path, "/passkey/login")
        XCTAssertEqual(
            URLComponents(url: url, resolvingAgainstBaseURL: false)?.fragment,
            "callback=simdeck://passkey&state=test-state"
        )
    }

    func testRegistersNativePasskeyWithAuthenticatedWebAuthnCeremony() async throws {
        let challengeData = Data([0xfb, 0xff, 0x00])
        let userID = Data([0x01, 0x02, 0x03])
        let credentialID = Data([0xfa, 0xfb, 0xfc])
        let clientData = Data("client-data".utf8)
        let attestation = Data("attestation".utf8)
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let passkeyClient = FakePasskeyRPCClient()
        passkeyClient.registrationOptions = PasskeyOptionsPayload(
            ceremonyID: "registration-1",
            optionsJSON: Data("""
            {"publicKey":{"challenge":"\(Base64URL.encode(challengeData))","rp":{"id":"deck.example.test","name":"Sim Deck"},"user":{"id":"\(Base64URL.encode(userID))","name":"admin","displayName":"Owner"}}}
            """.utf8)
        )
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: ScriptedServerNetwork { _ in Self.response(status: 500, body: "{}") },
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )

        let ceremony = try await manager.beginPasskeyRegistration()
        XCTAssertEqual(ceremony.id, "registration-1")
        XCTAssertEqual(ceremony.challenge.relyingPartyID, "deck.example.test")
        XCTAssertEqual(ceremony.challenge.challenge, challengeData)
        XCTAssertEqual(ceremony.challenge.userID, userID)
        XCTAssertEqual(ceremony.challenge.userName, "admin")

        try await manager.finishPasskeyRegistration(
            ceremonyID: ceremony.id,
            credential: PasskeyRegistrationCredential(
                credentialID: credentialID,
                clientDataJSON: clientData,
                attestationObject: attestation
            )
        )
        XCTAssertEqual(passkeyClient.registeredCeremonyID, "registration-1")
        XCTAssertEqual(passkeyClient.registrationAccessToken, "access")
        let credential = try XCTUnwrap(JSONSerialization.jsonObject(with: passkeyClient.registeredCredentialJSON) as? [String: Any])
        XCTAssertEqual(credential["id"] as? String, Base64URL.encode(credentialID))
        XCTAssertEqual(credential["rawId"] as? String, Base64URL.encode(credentialID))
        let response = try XCTUnwrap(credential["response"] as? [String: String])
        XCTAssertEqual(response["clientDataJSON"], Base64URL.encode(clientData))
        XCTAssertEqual(response["attestationObject"], Base64URL.encode(attestation))
    }

    func testSignsInWithNativePasskeyAndPersistsSession() async throws {
        let challengeData = Data([0xfe, 0xed, 0x01])
        let credentialID = Data([0xfa, 0xfb, 0xfc])
        let clientData = Data("client-data".utf8)
        let authenticatorData = Data("authenticator".utf8)
        let signature = Data("signature".utf8)
        let userID = Data([0x01, 0x02, 0x03])
        let store = MemoryServerSessionStore()
        let passkeyClient = FakePasskeyRPCClient()
        passkeyClient.loginOptions = PasskeyOptionsPayload(
            ceremonyID: "login-1",
            optionsJSON: Data("""
            {"publicKey":{"challenge":"\(Base64URL.encode(challengeData))","rpId":"deck.example.test"}}
            """.utf8)
        )
        let defaults = isolatedDefaults()
        let manager = ServerConnectionManager(
            network: ScriptedServerNetwork { _ in Self.response(status: 500, body: "{}") },
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )
        manager.address = "https://deck.example.test:54443"

        let ceremony = try await manager.beginPasskeyLogin()
        XCTAssertEqual(ceremony.challenge.relyingPartyID, "deck.example.test")
        XCTAssertEqual(ceremony.challenge.challenge, challengeData)
        try await manager.finishPasskeyLogin(
            ceremonyID: ceremony.id,
            credential: PasskeyAssertionCredential(
                credentialID: credentialID,
                clientDataJSON: clientData,
                authenticatorData: authenticatorData,
                signature: signature,
                userID: userID
            )
        )

        XCTAssertTrue(manager.isSignedIn)
        XCTAssertEqual(manager.state, .connected)
        XCTAssertTrue(String(decoding: try XCTUnwrap(store.value), as: UTF8.self).contains("passkey-access"))
        XCTAssertEqual(defaults.string(forKey: "sim-deck.server-address.v1"), manager.address)
        XCTAssertEqual(passkeyClient.loginCeremonyID, "login-1")
        let credential = try XCTUnwrap(JSONSerialization.jsonObject(with: passkeyClient.loginCredentialJSON) as? [String: Any])
        XCTAssertEqual(credential["id"] as? String, Base64URL.encode(credentialID))
        let credentialResponse = try XCTUnwrap(credential["response"] as? [String: String])
        XCTAssertEqual(credentialResponse["authenticatorData"], Base64URL.encode(authenticatorData))
        XCTAssertEqual(credentialResponse["signature"], Base64URL.encode(signature))
        XCTAssertEqual(credentialResponse["userHandle"], Base64URL.encode(userID))
    }

    func testRevokeCurrentSessionCallsLogoutAndClearsLocalSession() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let passkeyClient = FakePasskeyRPCClient()
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: ScriptedServerNetwork { _ in Self.response(status: 500, body: "{}") },
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )

        try await manager.revokeCurrentSession()

        XCTAssertFalse(manager.isSignedIn)
        XCTAssertEqual(manager.state, .disconnected)
        XCTAssertNil(store.value)
        XCTAssertEqual(passkeyClient.logoutAccessToken, "access")
    }

    func testSignInPersistsSessionAndLoadsEncryptedCommandMetadata() async throws {
        let deviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")!
        let futureDeviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-ffffffffffff")!
        let commandKey = Data(repeating: 7, count: 32)
        let identityKey = Data(repeating: 8, count: 32)
        let passkeyClient = FakePasskeyRPCClient()
        passkeyClient.bootstrapSession = ServerSession(
            accessToken: "access-one", refreshToken: "refresh-one",
            accessExpiresAt: Date(timeIntervalSince1970: 1_900_000_000),
            refreshExpiresAt: Date(timeIntervalSince1970: 2_000_000_000)
        )
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/relay.communication.v1.OwnerService/ListDevices"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-one")
                return Self.response(
                    status: 200,
                    body: """
                    {"devices":[{
                      "device_uuid":"\(deviceID.uuidString.lowercased())",
                      "numeric_id":2,
                      "profile_ciphertext":"",
                      "identity_public_key":"\(identityKey.base64EncodedString())",
                      "command_public_key":"\(commandKey.base64EncodedString())",
                      "paired_at":"2026-10-16T00:00:00Z",
                      "last_seen_at":"2026-10-16T00:00:30Z",
                      "radio_access_technology":"lte",
                      "signal_bars":3,
                      "radio_status_updated_at":"2026-10-16T00:00:40Z"
                    },{
                      "device_uuid":"\(futureDeviceID.uuidString.lowercased())",
                      "numeric_id":4,
                      "profile_ciphertext":"",
                      "identity_public_key":"\(identityKey.base64EncodedString())",
                      "command_public_key":"\(commandKey.base64EncodedString())",
                      "paired_at":"2026-10-16T00:00:00Z",
                      "last_seen_at":"2026-10-16T00:02:00Z"
                    }]}
                    """
                )
            default:
                XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.absoluteString ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let store = MemoryServerSessionStore()
        let defaults = isolatedDefaults()
        let manager = ServerConnectionManager(
            network: network,
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )
        manager.address = "https://deck.example.test"

        try await manager.signIn(password: "correct horse")
        let devices = try await manager.loadDevices(
            now: Date(timeIntervalSince1970: 1_792_108_860)
        )

        XCTAssertEqual(manager.state, .connected)
        XCTAssertNotNil(store.value)
        XCTAssertEqual(defaults.string(forKey: "sim-deck.server-address.v1"), manager.address)
        XCTAssertEqual(devices.count, 2)
        XCTAssertEqual(devices[0].id, deviceID)
        XCTAssertEqual(passkeyClient.bootstrapUsername, "admin")
        XCTAssertEqual(passkeyClient.bootstrapPassword, "correct horse")
        XCTAssertEqual(devices[0].numericID, 2)
        XCTAssertEqual(devices[0].commandPublicKey, commandKey)
        XCTAssertEqual(
            devices[0].radioStatus,
            CellularRadioStatus(technology: .lte, signalBars: 3)
        )
        let loadedIdentityKey = try await manager.eventVerificationKey(deviceID: deviceID)
        XCTAssertEqual(loadedIdentityKey, identityKey)
        XCTAssertTrue(devices[0].isOnline)
        XCTAssertFalse(devices[1].isOnline)
        XCTAssertNil(devices[1].radioStatus)
    }

    func testDeviceRadioStatusSuppressesStaleValuesAndRejectsInvalidBars() async throws {
        let staleDeviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000001")!
        let invalidBarsDeviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000002")!
        let commandKey = Data(repeating: 7, count: 32)
        let identityKey = Data(repeating: 8, count: 32)
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.url?.path, "/relay.communication.v1.OwnerService/ListDevices")
            return Self.response(
                status: 200,
                body: """
                {"devices":[{
                  "device_uuid":"\(staleDeviceID.uuidString.lowercased())",
                  "numeric_id":1,
                  "identity_public_key":"\(identityKey.base64EncodedString())",
                  "command_public_key":"\(commandKey.base64EncodedString())",
                  "last_seen_at":"2026-10-16T00:00:50Z",
                  "radio_access_technology":"lte",
                  "signal_bars":4,
                  "radio_status_updated_at":"2026-10-16T00:00:00Z"
                },{
                  "device_uuid":"\(invalidBarsDeviceID.uuidString.lowercased())",
                  "numeric_id":2,
                  "identity_public_key":"\(identityKey.base64EncodedString())",
                  "command_public_key":"\(commandKey.base64EncodedString())",
                  "last_seen_at":"2026-10-16T00:00:50Z",
                  "radio_access_technology":"nr_sa",
                  "signal_bars":9,
                  "radio_status_updated_at":"2026-10-16T00:00:50Z"
                }]}
                """
            )
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let devices = try await manager.loadDevices(
            now: Date(timeIntervalSince1970: 1_792_108_860)
        )

        XCTAssertTrue(devices.allSatisfy(\.isOnline))
        XCTAssertNil(devices[0].radioStatus)
        XCTAssertEqual(devices[1].radioStatus?.technology, .nrSA)
        XCTAssertNil(devices[1].radioStatus?.signalBars)
    }

    func testLoadsDistinctLineIDsForMultipleSlotsOnOneDevice() async throws {
        let deviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000011")!
        let firstSlotID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000012")!
        let secondSlotID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000013")!
        let simID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000014")!
        let key = Data(repeating: 7, count: 32)
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            Self.response(status: 200, body: """
            {"devices":[{
              "device_uuid":"\(deviceID.uuidString.lowercased())",
              "numeric_id":1,
              "identity_public_key":"\(key.base64EncodedString())",
              "command_public_key":"\(key.base64EncodedString())",
              "last_seen_at":"2026-10-16T00:00:50Z",
              "slots":[
                {"slot_id":"\(firstSlotID.uuidString.lowercased())","slot_index":1,"slot_key":"usb-a","numeric_id":1},
                {"slot_id":"\(secondSlotID.uuidString.lowercased())","slot_index":2,"slot_key":"usb-b","sim_id":"\(simID.uuidString.lowercased())","numeric_id":2}
              ]
            }]}
            """)
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let lines = try await manager.loadDevices(now: Date(timeIntervalSince1970: 1_792_108_860))

        XCTAssertEqual(lines.map(\.id), [firstSlotID, secondSlotID])
        XCTAssertEqual(lines.map(\.deviceID), [deviceID, deviceID])
        XCTAssertEqual(lines[1].simID, simID)
    }

    func testCommandTargetRefreshesStaleSlotAndSIMIdentifiersByStableSlotIdentity() async throws {
        let deviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000021")!
        let currentSlotID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000022")!
        let currentSIMID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000023")!
        let staleSlotID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000024")!
        let staleSIMID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000025")!
        let commandKey = Data(repeating: 9, count: 32)
        let lastSeenAt = ISO8601DateFormatter().string(from: Date().addingTimeInterval(-1))
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.url?.path, "/relay.communication.v1.OwnerService/ListDevices")
            return Self.response(status: 200, body: """
            {"devices":[{
              "device_uuid":"\(deviceID.uuidString.lowercased())",
              "numeric_id":1,
              "identity_public_key":"\(commandKey.base64EncodedString())",
              "command_public_key":"\(commandKey.base64EncodedString())",
              "last_seen_at":"\(lastSeenAt)",
              "slots":[{
                "slot_id":"\(currentSlotID.uuidString.lowercased())",
                "slot_index":1,
                "slot_key":"physical-slot-a",
                "sim_id":"\(currentSIMID.uuidString.lowercased())",
                "numeric_id":1
              }]
            }]}
            """)
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)
        let staleLine = DeviceLineIdentity(
            id: staleSlotID,
            deviceID: deviceID,
            slotID: staleSlotID,
            simID: staleSIMID,
            numericID: 1,
            slotKey: "physical-slot-a",
            slotIndex: 1,
            deviceName: "EC20F",
            lineName: "Line",
            phoneNumber: "13800000000",
            isOnline: true,
            commandPublicKey: commandKey
        )

        let target = try await manager.commandTarget(for: staleLine)

        XCTAssertEqual(target.deviceID, deviceID)
        XCTAssertEqual(target.slotID, currentSlotID)
        XCTAssertEqual(target.simID, currentSIMID)
        XCTAssertEqual(target.publicKey, commandKey)
    }

    func testLoadsAndDecryptsDeviceProfileWithVaultKey() async throws {
        let deviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-000000000003")!
        let key = try VaultKey(data: Data(repeating: 6, count: VaultKey.length))
        let profile = DeviceProfile(
            deviceName: "Living Room EC20",
            lineName: "Personal",
            phoneNumber: "13500000000"
        )
        let ciphertext = try DeviceProfileCodec.seal(profile, using: key)
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            Self.response(
                status: 200,
                body: """
                {"devices":[{
                  "device_uuid":"\(deviceID.uuidString.lowercased())",
                  "numeric_id":2,
                  "profile_ciphertext":"\(ciphertext.base64EncodedString())",
                  "identity_public_key":"",
                  "paired_at":"2026-10-16T00:00:00Z"
                }]}
                """
            )
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let devices = try await manager.loadDevices(profileKey: key)

        XCTAssertEqual(devices.first?.deviceName, profile.deviceName)
        XCTAssertEqual(devices.first?.lineName, profile.lineName)
        XCTAssertEqual(devices.first?.phoneNumber, profile.phoneNumber)
    }

    func testManagesAppControlledDeviceDiscovery() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000401")!
        let identityKey = Data(repeating: 4, count: 32)
        let commandKey = Data(repeating: 5, count: 32)
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let discoveryPath = "/relay.communication.v1.OwnerService/RejectDeviceDiscovery"
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/relay.communication.v1.OwnerService/OpenDeviceDiscovery"):
                return Self.response(
                    status: 201,
                    body: """
                    {"active":true,"session":{
                      "opened_at":"2026-10-16T13:00:00Z",
                      "expires_at":"2026-10-16T13:10:00Z"
                    }}
                    """
                )
            case ("POST", "/relay.communication.v1.OwnerService/GetDeviceDiscoverySession"):
                return Self.response(
                    status: 200,
                    body: """
                    {"active":true,"session":{
                      "opened_at":"2026-10-16T13:00:00Z",
                      "expires_at":"2026-10-16T13:10:00Z"
                    }}
                    """
                )
            case ("POST", "/relay.communication.v1.OwnerService/ListDeviceDiscoveries"):
                return Self.response(
                    status: 200,
                    body: """
                    {"discoveries":[{
                      "device_uuid":"\(deviceID.uuidString.lowercased())",
                      "identity_public_key":"\(identityKey.base64EncodedString())",
                      "command_public_key":"\(commandKey.base64EncodedString())",
                      "status":"pending",
                      "first_seen_at":"2026-10-16T13:01:00Z",
                      "last_seen_at":"2026-10-16T13:02:00Z",
                      "expires_at":"2026-10-16T13:10:00Z"
                    }]}
                    """
                )
            case ("POST", "/relay.communication.v1.OwnerService/ApproveDeviceDiscovery"):
                return Self.response(
                    status: 200,
                    body: """
                    {"device":{"device_uuid":"\(deviceID.uuidString.lowercased())","numeric_id":2}}
                    """
                )
            case ("POST", discoveryPath), ("POST", "/relay.communication.v1.OwnerService/CloseDeviceDiscovery"):
                return Self.response(status: 204, body: "")
            default:
                XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.path ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let opened = try await manager.openDeviceDiscovery()
        let loaded = try await manager.deviceDiscoverySession()
        let discoveries = try await manager.discoveredDevices()
        let approved = try await manager.approveDiscoveredDevice(id: deviceID)
        try await manager.rejectDiscoveredDevice(id: deviceID)
        try await manager.closeDeviceDiscovery()

        XCTAssertTrue(opened.active)
        XCTAssertEqual(loaded, opened)
        XCTAssertEqual(discoveries.first?.deviceUUID, deviceID)
        XCTAssertEqual(discoveries.first?.identityPublicKey, identityKey)
        XCTAssertEqual(discoveries.first?.commandPublicKey, commandKey)
        XCTAssertEqual(discoveries.first?.status, .pending)
        XCTAssertEqual(approved, ApprovedDevice(deviceUUID: deviceID, numericID: 2))
        XCTAssertEqual(
            network.requests.map { "\($0.httpMethod ?? "") \($0.url?.path ?? "")" },
            [
                "POST /relay.communication.v1.OwnerService/OpenDeviceDiscovery",
                "POST /relay.communication.v1.OwnerService/GetDeviceDiscoverySession",
                "POST /relay.communication.v1.OwnerService/ListDeviceDiscoveries",
                "POST /relay.communication.v1.OwnerService/ApproveDeviceDiscovery",
                "POST /relay.communication.v1.OwnerService/RejectDeviceDiscovery",
                "POST /relay.communication.v1.OwnerService/CloseDeviceDiscovery",
            ]
        )
    }

    func testDeviceDiscoveryMapsFailureAndAllowsInactiveSessionWithoutMetadata() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            if request.url?.path == "/relay.communication.v1.OwnerService/GetDeviceDiscoverySession" {
                return Self.response(status: 200, body: "{\"active\":false}")
            }
            return Self.response(status: 410, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let session = try await manager.deviceDiscoverySession()
        XCTAssertFalse(session.active)
        XCTAssertNil(session.session)

        do {
            _ = try await manager.approveDiscoveredDevice(id: UUID())
            XCTFail("Expected an expired discovery to fail")
        } catch {
            XCTAssertNotNil(error as? Connect.ConnectError)
        }
    }

    func testUpdatesEncryptedDeviceAndSIMProfilesThenRevokesDevice() async throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let ciphertext = Data("opaque encrypted profile".utf8)
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let deviceProfile = "/relay.communication.v1.OwnerService/UpdateDeviceProfile"
            let deviceNumericID = "/relay.communication.v1.OwnerService/UpdateDeviceNumericId"
            let simProfile = "/relay.communication.v1.OwnerService/UpdateSimProfile"
            let simNumericID = "/relay.communication.v1.OwnerService/UpdateSimNumericId"
            let revokeDevice = "/relay.communication.v1.OwnerService/RevokeDevice"
            switch (request.httpMethod, request.url?.path) {
            case ("POST", deviceProfile), ("POST", simProfile):
                let body = try XCTUnwrap(request.httpBody)
                let decoded = try Relay_Communication_V1_UpdateDeviceProfileRequest(serializedBytes: body)
                XCTAssertEqual(decoded.profileCiphertext, ciphertext)
            case ("POST", deviceNumericID), ("POST", simNumericID):
                let body = try XCTUnwrap(request.httpBody)
                let decoded = try Relay_Communication_V1_UpdateDeviceNumericIdRequest(serializedBytes: body)
                XCTAssertEqual(decoded.numericID, 7)
            case ("POST", revokeDevice):
                XCTAssertEqual(request.url?.path, revokeDevice)
            default:
                XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.path ?? "")")
            }
            return Self.response(status: 204, body: "")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        try await manager.updateDeviceProfile(deviceID: deviceID, profileCiphertext: ciphertext)
        try await manager.updateDeviceNumericID(deviceID: deviceID, numericID: 7)
        try await manager.updateSIMProfile(simID: simID, profileCiphertext: ciphertext)
        try await manager.updateSIMNumericID(simID: simID, numericID: 7)
        try await manager.revokeDevice(deviceID: deviceID)

        XCTAssertEqual(
            network.requests.compactMap(\.url?.path),
            [
                "/relay.communication.v1.OwnerService/UpdateDeviceProfile",
                "/relay.communication.v1.OwnerService/UpdateDeviceNumericId",
                "/relay.communication.v1.OwnerService/UpdateSimProfile",
                "/relay.communication.v1.OwnerService/UpdateSimNumericId",
                "/relay.communication.v1.OwnerService/RevokeDevice"
            ]
        )
    }

    func testListsSIMCardsAndDeletesOnlyTheSelectedRecord() async throws {
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/relay.communication.v1.OwnerService/ListSimCards"):
                return Self.response(status: 200, body: """
                {"sims":[{
                  "sim_id":"\(simID.uuidString.lowercased())",
                  "numeric_id":3,
                  "last_seen_at":"2026-10-16T00:00:00Z"
                }]}
                """)
            case ("POST", "/relay.communication.v1.OwnerService/RetireSimCard"):
                return Self.response(status: 204, body: "")
            default:
                XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url?.path ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let cards = try await manager.loadSIMCards()
        try await manager.retireSIMCard(simID: simID)

        XCTAssertEqual(cards, [ManagedSIM(id: simID, numericID: 3, assignedDeviceID: nil, assignedSlotID: nil, lastSeenAt: Date(timeIntervalSince1970: 1_792_108_800), lineName: "", phoneNumber: "")])
        XCTAssertTrue(cards[0].isOffline)
    }

    func testMapsDeviceManagementFailureStatus() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in Self.response(status: 409, body: "{}") }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        do {
            try await manager.updateDeviceNumericID(deviceID: UUID(), numericID: 2)
            XCTFail("Expected the duplicate SIM ID to fail")
        } catch {
            XCTAssertEqual((error as? Connect.ConnectError)?.code, .unknown)
        }
    }

    func testExpiredAccessTokenRefreshesBeforeLoadingDevices() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(
                accessToken: "expired-access",
                refreshToken: "refresh-one",
                accessExpiresAt: "2020-01-01T00:00:00Z",
                refreshExpiresAt: "2099-01-01T00:00:00Z"
            ).utf8)
        )
        let passkeyClient = FakePasskeyRPCClient()
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/relay.communication.v1.OwnerService/ListDevices"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fresh-access")
                return Self.response(status: 200, body: "{\"devices\":[]}")
            default:
                XCTFail("Unexpected request: \(request.url?.absoluteString ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: network,
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )

        let devices = try await manager.loadDevices()

        XCTAssertTrue(devices.isEmpty)
        XCTAssertEqual(network.requests.map(\.url?.path), ["/relay.communication.v1.OwnerService/ListDevices"])
        XCTAssertEqual(passkeyClient.refreshToken, "refresh-one")
        XCTAssertTrue(String(decoding: try XCTUnwrap(store.value), as: UTF8.self).contains("fresh-access"))
    }

    func testConcurrentExpiredRequestsShareOneRefresh() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(
                accessToken: "expired-access",
                refreshToken: "refresh-one",
                accessExpiresAt: "2020-01-01T00:00:00Z",
                refreshExpiresAt: "2099-01-01T00:00:00Z"
            ).utf8)
        )
        let refreshGate = AsyncRequestGate()
        let refreshStarted = expectation(description: "Refresh request started")
        let passkeyClient = FakePasskeyRPCClient()
        passkeyClient.refreshHandler = { refreshToken in
            XCTAssertEqual(refreshToken, "refresh-one")
            refreshStarted.fulfill()
            await refreshGate.wait()
            return ServerSession(
                accessToken: "fresh-access", refreshToken: "refresh-two",
                accessExpiresAt: Date(timeIntervalSince1970: 1_900_000_000),
                refreshExpiresAt: Date(timeIntervalSince1970: 2_000_000_000)
            )
        }
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/relay.communication.v1.OwnerService/ListDevices"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fresh-access")
                return Self.response(status: 200, body: "{\"devices\":[]}")
            default:
                XCTFail("Unexpected request: \(request.url?.absoluteString ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: network,
            sessionStore: store,
            defaults: defaults,
            passkeyRPCClientFactory: FakePasskeyRPCClientFactory(client: passkeyClient)
        )

        let firstRequest = Task { try await manager.loadDevices() }
        await fulfillment(of: [refreshStarted], timeout: 1)
        let secondRequest = Task { try await manager.loadDevices() }
        for _ in 0..<20 {
            await Task.yield()
        }
        await refreshGate.open()

        _ = try await firstRequest.value
        _ = try await secondRequest.value
        XCTAssertEqual(
            network.requests.filter { $0.url?.path == "/relay.communication.v1.AuthenticationService/Refresh" }.count, 0
        )
        XCTAssertEqual(
            network.requests.filter { $0.url?.path == "/relay.communication.v1.OwnerService/ListDevices" }.count,
            2
        )
    }

    func testInvalidAddressFailsBeforeSendingCredentials() async {
        let network = ScriptedServerNetwork { _ in
            XCTFail("Invalid addresses must not reach the network")
            return Self.response(status: 500, body: "{}")
        }
        let manager = ServerConnectionManager(
            network: network,
            sessionStore: MemoryServerSessionStore(),
            defaults: isolatedDefaults()
        )
        manager.address = "http://deck.example.test"

        do {
            try await manager.signIn(password: "secret")
            XCTFail("Expected HTTPS validation to fail")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .invalidAddress)
        }

        XCTAssertEqual(manager.state, .failed)
        XCTAssertTrue(network.requests.isEmpty)
    }

    func testSignOutDeletesLocalSession() throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let manager = ServerConnectionManager(
            network: ScriptedServerNetwork { _ in Self.response(status: 500, body: "{}") },
            sessionStore: store,
            defaults: isolatedDefaults()
        )

        XCTAssertTrue(manager.isSignedIn)
        try manager.signOut()

        XCTAssertFalse(manager.isSignedIn)
        XCTAssertNil(store.value)
        XCTAssertEqual(manager.state, .disconnected)
    }

    func testDeviceLoadFailureUpdatesVisibleConnectionState() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            Self.response(status: 503, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        do {
            _ = try await manager.loadDevices()
            XCTFail("Expected the device request to fail")
        } catch {
            XCTAssertEqual((error as? Connect.ConnectError)?.code, .unavailable)
        }

        XCTAssertEqual(manager.state, .failed)
        XCTAssertNotNil(manager.lastErrorDescription)
    }

    func testEncryptedEventCursorIsAuthenticatedAndDecoded() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let eventID = UUID(uuidString: "01900000-0000-7000-8000-000000000011")!
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
        let client = TestCommunicationSyncClient(page: EncryptedEventPage(events: [EncryptedEventEnvelope(
            id: eventID,
            sequence: 42,
            type: "call.state",
            deviceUUID: deviceID,
            occurredAt: Date(timeIntervalSince1970: 1_797_000_550.123),
            keyEpoch: 1,
            nonce: Data(repeating: 0, count: 12),
            ciphertext: Data([1, 2, 3]),
            signature: Data([4, 5, 6])
        )], nextSequence: 42))
        let clientFactory = TestCommunicationSyncClientFactory(client: client)
        let network = ScriptedServerNetwork { _ in
            XCTFail("Event synchronization must use Connect RPC")
            return Self.response(status: 500, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: network,
            sessionStore: store,
            defaults: defaults,
            communicationSyncClientFactory: clientFactory
        )

        let page = try await manager.encryptedEvents(afterSequence: 41)

        XCTAssertEqual(page.nextSequence, 42)
        XCTAssertEqual(page.events.first?.id, eventID)
        XCTAssertEqual(page.events.first?.deviceUUID, deviceID)
        XCTAssertEqual(client.pullCursor, 41)
        XCTAssertEqual(client.pullToken, "access")
        XCTAssertEqual(clientFactory.baseURL?.absoluteString, "https://deck.example.test")
    }

    func testRegistersInstallationIdentityWithAuthenticatedRequest() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let installationID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let publicKey = Data(repeating: 7, count: 32)
        let keyAgreementPublicKey = Data(repeating: 8, count: 32)
        let wrappedKey = Data("wrapped vault key".utf8)
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/relay.communication.v1.OwnerService/RegisterAppInstallation")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let body = try XCTUnwrap(request.httpBody)
            let object = try Relay_Communication_V1_RegisterAppInstallationRequest(serializedBytes: body)
            XCTAssertEqual(
                UUID(uuidString: object.installationID),
                installationID
            )
            XCTAssertEqual(object.identityPublicKey, publicKey)
            XCTAssertEqual(object.keyAgreementPublicKey, keyAgreementPublicKey)
            XCTAssertEqual(object.wrappedVaultKey, wrappedKey)
            return Self.response(
                status: 201,
                body: """
                {"installation":{
                  "installationId":"\(installationID.uuidString.lowercased())",
                  "identityPublicKey":"\(publicKey.base64EncodedString())",
                  "keyAgreementPublicKey":"\(keyAgreementPublicKey.base64EncodedString())",
                  "wrappedVaultKey":"\(wrappedKey.base64EncodedString())",
                  "approvedAt":"2026-10-16T13:00:00Z",
                  "createdAt":"2026-10-16T13:00:00Z"
                },"created":true}
                """
            )
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let trust = try await manager.registerAppInstallation(
            AppInstallationRegistration(
                id: installationID,
                identityPublicKey: publicKey,
                keyAgreementPublicKey: keyAgreementPublicKey,
                wrappedVaultKey: wrappedKey
            )
        )

        XCTAssertEqual(trust.id, installationID)
        XCTAssertEqual(trust.identityPublicKey, publicKey)
        XCTAssertEqual(trust.keyAgreementPublicKey, keyAgreementPublicKey)
        XCTAssertEqual(trust.wrappedVaultKey, wrappedKey)
        XCTAssertTrue(trust.isApproved)
    }

    func testRejectsInvalidInstallationPublicKeyBeforeNetworkRequest() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            XCTFail("Invalid installation keys must not reach the network")
            return Self.response(status: 500, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        do {
            _ = try await manager.registerAppInstallation(
                AppInstallationRegistration(
                    id: UUID(),
                    identityPublicKey: Data(repeating: 1, count: 31),
                    keyAgreementPublicKey: Data(repeating: 2, count: 32),
                    wrappedVaultKey: nil
                )
            )
            XCTFail("Expected the malformed key to fail")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .invalidResponse)
        }

        do {
            _ = try await manager.registerAppInstallation(
                AppInstallationRegistration(
                    id: UUID(),
                    identityPublicKey: Data(repeating: 1, count: 32),
                    keyAgreementPublicKey: Data(repeating: 2, count: 31),
                    wrappedVaultKey: nil
                )
            )
            XCTFail("Expected the malformed agreement key to fail")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .invalidResponse)
        }

        XCTAssertTrue(network.requests.isEmpty)
    }

    func testClaimsIncomingCallForCurrentInstallation() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let notificationID = UUID(uuidString: "01900000-0000-7000-8000-000000000010")!
        let installationID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(
                request.url?.path,
                "/relay.communication.v1.OwnerService/ClaimIncomingCall"
            )
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let body = try XCTUnwrap(request.httpBody)
            let object = try Relay_Communication_V1_ClaimIncomingCallRequest(serializedBytes: body)
            XCTAssertEqual(
                UUID(uuidString: object.installationID),
                installationID
            )
            return Self.response(
                status: 200,
                body: """
                {"notificationId":"\(notificationID.uuidString.lowercased())",
                  "installationId":"\(installationID.uuidString.lowercased())",
                  "claimedAt":"2026-10-16T13:00:00Z",
                  "won":true
                }
                """
            )
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let claim = try await manager.claimIncomingCall(
            notificationID: notificationID,
            installationID: installationID
        )

        XCTAssertEqual(claim.notificationID, notificationID)
        XCTAssertEqual(claim.installationID, installationID)
        XCTAssertTrue(claim.won)
    }

    func testMapsIncomingCallClaimFailure() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            Self.response(status: 404, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        do {
            _ = try await manager.claimIncomingCall(
                notificationID: UUID(),
                installationID: UUID()
            )
            XCTFail("Expected the unavailable call to fail")
        } catch {
            XCTAssertEqual((error as? Connect.ConnectError)?.code, .unimplemented)
        }
    }

    func testRegistersVoIPPushTokenForInstallation() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let installationID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let token = Data((0..<32).map(UInt8.init))
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(
                request.url?.path,
                "/relay.communication.v1.OwnerService/UpsertPushToken"
            )
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let body = try XCTUnwrap(request.httpBody)
            let object = try Relay_Communication_V1_UpsertPushTokenRequest(serializedBytes: body)
            XCTAssertEqual(object.environment, "sandbox")
            XCTAssertEqual(object.token, token)
            return Self.response(status: 204, body: "")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        try await manager.registerPushToken(
            token,
            kind: .voip,
            environment: .sandbox,
            installationID: installationID
        )

        XCTAssertEqual(network.requests.count, 1)
    }

    func testRejectsEmptyPushTokenBeforeNetworkRequest() async {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { _ in
            XCTFail("Empty push tokens must not reach the network")
            return Self.response(status: 500, body: "{}")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        do {
            try await manager.registerPushToken(
                Data(),
                kind: .voip,
                environment: .production,
                installationID: UUID()
            )
            XCTFail("Expected empty token validation to fail")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .invalidResponse)
        }

        XCTAssertTrue(network.requests.isEmpty)
    }

    func testDeletesInvalidatedVoIPPushTokenForInstallation() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let installationID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(
                request.url?.path,
                "/relay.communication.v1.OwnerService/DeletePushToken"
            )
            let body = try XCTUnwrap(request.httpBody)
            let object = try Relay_Communication_V1_DeletePushTokenRequest(serializedBytes: body)
            XCTAssertEqual(object.environment, "sandbox")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            return Self.response(status: 204, body: "")
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        try await manager.unregisterPushToken(
            kind: .voip,
            environment: .sandbox,
            installationID: installationID
        )

        XCTAssertEqual(network.requests.count, 1)
    }

    private func isolatedDefaults() -> UserDefaults {
        let suiteName = "ServerConnectionManagerTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defaults.removePersistentDomain(forName: suiteName)
        return defaults
    }

    private static func response(status: Int, body: String) -> (Data, HTTPURLResponse) {
        let response = HTTPURLResponse(
            url: URL(string: "https://deck.example.test")!,
            statusCode: status,
            httpVersion: nil,
            headerFields: nil
        )!
        return (Data(body.utf8), response)
    }

    private static func sessionJSON(
        accessToken: String,
        refreshToken: String,
        accessExpiresAt: String = "2098-01-01T00:00:00Z",
        refreshExpiresAt: String = "2099-01-01T00:00:00Z"
    ) -> String {
        """
        {"access_token":"\(accessToken)","refresh_token":"\(refreshToken)","access_expires_at":"\(accessExpiresAt)","refresh_expires_at":"\(refreshExpiresAt)"}
        """
    }
}

@MainActor
private final class TestCommunicationSyncClient: CommunicationSyncClient {
    let page: EncryptedEventPage
    private(set) var pullCursor: Int64?
    private(set) var pullToken: String?

    init(page: EncryptedEventPage) {
        self.page = page
    }

    func pullEvents(afterSequence: Int64, accessToken: String) async throws -> EncryptedEventPage {
        pullCursor = afterSequence
        pullToken = accessToken
        return page
    }

    func watchEvents(afterSequence: Int64, accessToken: String) throws -> any EncryptedEventStream {
        throw ServerConnectionError.eventStreamUnavailable
    }

    func getHistorySnapshot(kind: Relay_Communication_V1_HistoryKind, accessToken: String) async throws -> HistorySnapshotPayload {
        HistorySnapshotPayload(revision: 0, ciphertext: Data())
    }
}

@MainActor
private final class TestCommunicationSyncClientFactory: CommunicationSyncClientCreating {
    private let client: any CommunicationSyncClient
    private(set) var baseURL: URL?

    init(client: any CommunicationSyncClient) {
        self.client = client
    }

    func makeClient(baseURL: URL) -> any CommunicationSyncClient {
        self.baseURL = baseURL
        return client
    }
}

@MainActor
final class NetworkOwnerRPCClientFactory: OwnerRPCClientCreating {
    private let network: any ServerNetworking

    init(network: any ServerNetworking) { self.network = network }

    func makeClient(baseURL: URL) -> any OwnerRPCClient {
        NetworkOwnerRPCClient(network: network, baseURL: baseURL)
    }
}

@MainActor
private final class NetworkOwnerRPCClient: OwnerRPCClient {
    let service: Relay_Communication_V1_OwnerServiceClientInterface

    init(network: any ServerNetworking, baseURL: URL) {
        let httpClient = NetworkOwnerHTTPClient(network: network, baseURL: baseURL)
        let config = ProtocolClientConfig(host: baseURL.absoluteString, codec: ProtoCodec())
        service = Relay_Communication_V1_OwnerServiceClient(
            client: ProtocolClient(httpClient: httpClient, config: config)
        )
    }
}

private final class NetworkOwnerHTTPClient: HTTPClientInterface, @unchecked Sendable {
    private let network: any ServerNetworking

    init(network: any ServerNetworking, baseURL: URL) {
        self.network = network
    }

    @discardableResult
    func unary(
        request: HTTPRequest<Data?>,
        onMetrics: @escaping @Sendable (HTTPMetrics) -> Void,
        onResponse: @escaping @Sendable (HTTPResponse) -> Void
    ) -> Cancelable {
        let task = Task {
            do {
                let requestPath = request.url.path
                var scriptedRequest = URLRequest(url: request.url)
                scriptedRequest.httpMethod = "POST"
                scriptedRequest.httpBody = request.message
                for (name, values) in request.headers {
                    scriptedRequest.setValue(values.joined(separator: ","), forHTTPHeaderField: name)
                }
                let (data, response) = try await network.data(for: scriptedRequest)
                onMetrics(HTTPMetrics(taskMetrics: nil))
                let responseData = response.statusCode < 300 ? try Self.responseProtoData(for: requestPath, json: data.isEmpty ? Data("{}".utf8) : data) : nil
                onResponse(HTTPResponse(
                    code: response.statusCode < 300 ? .ok : .fromHTTPStatus(response.statusCode),
                    headers: response.statusCode < 300 ? ["content-type": ["application/proto"]] : [:],
                    message: responseData,
                    trailers: [:],
                    error: nil,
                    tracingInfo: .init(httpStatus: response.statusCode)
                ))
            } catch {
                onMetrics(HTTPMetrics(taskMetrics: nil))
                onResponse(HTTPResponse(
                    code: .unavailable,
                    headers: [:],
                    message: nil,
                    trailers: [:],
                    error: error,
                    tracingInfo: nil
                ))
            }
        }
        return Cancelable(cancel: { task.cancel() })
    }

    func stream(request: HTTPRequest<Data?>, responseCallbacks: ResponseCallbacks) -> RequestCallbacks<Data> {
        fatalError("Owner RPC test transport does not support streams")
    }

    private static func responseProtoData(for path: String, json: Data) throws -> Data {
        let method = path.components(separatedBy: "/").last ?? ""
        var options = JSONDecodingOptions()
        options.ignoreUnknownFields = true
        let response: any SwiftProtobuf.Message = switch method {
        case "ListDevices": try Relay_Communication_V1_ListDevicesResponse(jsonUTF8Data: json, options: options)
        case "ListSimCards": try Relay_Communication_V1_ListSimCardsResponse(jsonUTF8Data: json, options: options)
        case "RetireSimCard": try Relay_Communication_V1_RetireSimCardResponse(jsonUTF8Data: json, options: options)
        case "UpdateDeviceNumericId": try Relay_Communication_V1_UpdateDeviceNumericIdResponse(jsonUTF8Data: json, options: options)
        case "UpdateDeviceProfile": try Relay_Communication_V1_UpdateDeviceProfileResponse(jsonUTF8Data: json, options: options)
        case "UpdateSimNumericId": try Relay_Communication_V1_UpdateSimNumericIdResponse(jsonUTF8Data: json, options: options)
        case "UpdateSimProfile": try Relay_Communication_V1_UpdateSimProfileResponse(jsonUTF8Data: json, options: options)
        case "RevokeDevice": try Relay_Communication_V1_RevokeDeviceResponse(jsonUTF8Data: json, options: options)
        case "OpenDeviceDiscovery": try Relay_Communication_V1_OpenDeviceDiscoveryResponse(jsonUTF8Data: json, options: options)
        case "GetDeviceDiscoverySession": try Relay_Communication_V1_GetDeviceDiscoverySessionResponse(jsonUTF8Data: json, options: options)
        case "CloseDeviceDiscovery": try Relay_Communication_V1_CloseDeviceDiscoveryResponse(jsonUTF8Data: json, options: options)
        case "ListDeviceDiscoveries": try Relay_Communication_V1_ListDeviceDiscoveriesResponse(jsonUTF8Data: json, options: options)
        case "ApproveDeviceDiscovery": try Relay_Communication_V1_ApproveDeviceDiscoveryResponse(jsonUTF8Data: json, options: options)
        case "RejectDeviceDiscovery": try Relay_Communication_V1_RejectDeviceDiscoveryResponse(jsonUTF8Data: json, options: options)
        case "RegisterAppInstallation": try Relay_Communication_V1_RegisterAppInstallationResponse(jsonUTF8Data: json, options: options)
        case "UpsertPushToken": try Relay_Communication_V1_UpsertPushTokenResponse(jsonUTF8Data: json, options: options)
        case "DeletePushToken": try Relay_Communication_V1_DeletePushTokenResponse(jsonUTF8Data: json, options: options)
        case "ClaimIncomingCall": try Relay_Communication_V1_ClaimIncomingCallResponse(jsonUTF8Data: json, options: options)
        case "EnqueueCommand": try Relay_Communication_V1_EnqueueCommandResponse(jsonUTF8Data: json, options: options)
        case "GetCommandStatus": try Relay_Communication_V1_GetCommandStatusResponse(jsonUTF8Data: json, options: options)
        case "GetCommandRecipient": try Relay_Communication_V1_GetCommandRecipientResponse(jsonUTF8Data: json, options: options)
        case "GetTurnCredentials": try Relay_Communication_V1_GetTurnCredentialsResponse(jsonUTF8Data: json, options: options)
        default: fatalError("Unknown owner RPC method: \(method)")
        }
        return try response.serializedData()
    }

}

@MainActor
private final class ScriptedServerNetwork: ServerNetworking {
    private(set) var requests: [URLRequest] = []
    private let handler: (URLRequest) async throws -> (Data, HTTPURLResponse)

    init(handler: @escaping (URLRequest) async throws -> (Data, HTTPURLResponse)) {
        self.handler = handler
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requests.append(request)
        return try await handler(request)
    }

}

extension ServerNetworking {
    func ownerRPCClientFactory() -> (any OwnerRPCClientCreating)? {
        NetworkOwnerRPCClientFactory(network: self)
    }
}

private actor AsyncRequestGate {
    private var isOpen = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        guard !isOpen else { return }
        await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }

    func open() {
        isOpen = true
        let pending = waiters
        waiters.removeAll()
        for waiter in pending {
            waiter.resume()
        }
    }
}

private final class MemoryServerSessionStore: SecureValueStoring, @unchecked Sendable {
    private let lock = NSLock()
    private(set) var value: Data?

    init(value: Data? = nil) {
        self.value = value
    }

    func read() -> Data? {
        lock.withLock { value }
    }

    func write(_ value: Data) {
        lock.withLock { self.value = value }
    }

    func delete() {
        lock.withLock { value = nil }
    }
}
