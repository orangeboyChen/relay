import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class ServerConnectionManagerTests: XCTestCase {
    func testSignInPersistsSessionAndLoadsEncryptedCommandMetadata() async throws {
        let deviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")!
        let futureDeviceID = UUID(uuidString: "aaaaaaaa-bbbb-cccc-dddd-ffffffffffff")!
        let commandKey = Data(repeating: 7, count: 32)
        let identityKey = Data(repeating: 8, count: 32)
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/v1/auth/bootstrap"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
                XCTAssertTrue(String(decoding: request.httpBody ?? Data(), as: UTF8.self).contains("correct horse"))
                return Self.response(
                    status: 201,
                    body: Self.sessionJSON(accessToken: "access-one", refreshToken: "refresh-one")
                )
            case ("GET", "/v1/devices"):
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
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)
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
            XCTAssertEqual(request.url?.path, "/v1/devices")
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
            let discoveryPath = "/v1/device-discoveries/\(deviceID.uuidString.lowercased())"
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/v1/device-discovery-session"):
                return Self.response(
                    status: 201,
                    body: """
                    {"active":true,"session":{
                      "opened_at":"2026-10-16T13:00:00Z",
                      "expires_at":"2026-10-16T13:10:00Z"
                    }}
                    """
                )
            case ("GET", "/v1/device-discovery-session"):
                return Self.response(
                    status: 200,
                    body: """
                    {"active":true,"session":{
                      "opened_at":"2026-10-16T13:00:00Z",
                      "expires_at":"2026-10-16T13:10:00Z"
                    }}
                    """
                )
            case ("GET", "/v1/device-discoveries"):
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
            case ("POST", "\(discoveryPath)/approve"):
                return Self.response(
                    status: 200,
                    body: """
                    {"device":{"device_uuid":"\(deviceID.uuidString.lowercased())","numeric_id":2}}
                    """
                )
            case ("DELETE", discoveryPath), ("DELETE", "/v1/device-discovery-session"):
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
                "POST /v1/device-discovery-session",
                "GET /v1/device-discovery-session",
                "GET /v1/device-discoveries",
                "POST /v1/device-discoveries/\(deviceID.uuidString.lowercased())/approve",
                "DELETE /v1/device-discoveries/\(deviceID.uuidString.lowercased())",
                "DELETE /v1/device-discovery-session",
            ]
        )
    }

    func testDeviceDiscoveryMapsFailureAndAllowsInactiveSessionWithoutMetadata() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let network = ScriptedServerNetwork { request in
            if request.url?.path == "/v1/device-discovery-session" {
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
            XCTAssertEqual(error as? ServerConnectionError, .requestFailed(410))
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
            let expectedBase = "/v1/devices/\(deviceID.uuidString.lowercased())"
            let expectedSIMBase = "/v1/sims/\(simID.uuidString.lowercased())"
            switch (request.httpMethod, request.url?.path) {
            case ("PUT", "\(expectedBase)/profile"), ("PUT", "\(expectedSIMBase)/profile"):
                let object = try XCTUnwrap(
                    JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
                )
                XCTAssertEqual(
                    object["profile_ciphertext"] as? String,
                    ciphertext.base64EncodedString()
                )
                XCTAssertEqual(object.count, 1)
            case ("PATCH", "\(expectedBase)/numeric-id"), ("PATCH", "\(expectedSIMBase)/numeric-id"):
                let object = try XCTUnwrap(
                    JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
                )
                XCTAssertEqual(object["numeric_id"] as? Int, 7)
            case ("DELETE", expectedBase):
                XCTAssertNil(request.httpBody)
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
                "/v1/devices/\(deviceID.uuidString.lowercased())/profile",
                "/v1/devices/\(deviceID.uuidString.lowercased())/numeric-id",
                "/v1/sims/\(simID.uuidString.lowercased())/profile",
                "/v1/sims/\(simID.uuidString.lowercased())/numeric-id",
                "/v1/devices/\(deviceID.uuidString.lowercased())"
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
            case ("GET", "/v1/sims"):
                return Self.response(status: 200, body: """
                {"sims":[{
                  "sim_id":"\(simID.uuidString.lowercased())",
                  "numeric_id":3,
                  "last_seen_at":"2026-10-16T00:00:00Z"
                }]}
                """)
            case ("DELETE", "/v1/sims/\(simID.uuidString.lowercased())"):
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
            XCTAssertEqual(error as? ServerConnectionError, .requestFailed(409))
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
        let network = ScriptedServerNetwork { request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/v1/auth/refresh"):
                XCTAssertEqual(
                    String(decoding: request.httpBody ?? Data(), as: UTF8.self),
                    "{\"refresh_token\":\"refresh-one\"}"
                )
                return Self.response(
                    status: 200,
                    body: Self.sessionJSON(accessToken: "fresh-access", refreshToken: "refresh-two")
                )
            case ("GET", "/v1/devices"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fresh-access")
                return Self.response(status: 200, body: "{\"devices\":[]}")
            default:
                XCTFail("Unexpected request: \(request.url?.absoluteString ?? "")")
                return Self.response(status: 500, body: "{}")
            }
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let devices = try await manager.loadDevices()

        XCTAssertTrue(devices.isEmpty)
        XCTAssertEqual(network.requests.map(\.url?.path), ["/v1/auth/refresh", "/v1/devices"])
        XCTAssertTrue(String(decoding: try XCTUnwrap(store.value), as: UTF8.self).contains("fresh-access"))
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
            XCTAssertEqual(error as? ServerConnectionError, .requestFailed(503))
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
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.url?.path, "/v1/events")
            XCTAssertEqual(request.url?.query, "after_sequence=41&limit=100")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            return Self.response(
                status: 200,
                body: """
                {"events":[{
                  "id":"\(eventID.uuidString.lowercased())",
                  "sequence":42,
                  "type":"call.state",
                  "device_uuid":"\(deviceID.uuidString.lowercased())",
                  "occurred_at":"2026-10-16T08:09:10.123Z",
                  "key_epoch":1,
                  "nonce":"AAAAAAAAAAAAAAAA",
                  "ciphertext":"AQID",
                  "signature":"BAUG"
                }],"next_sequence":42}
                """
            )
        }
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(network: network, sessionStore: store, defaults: defaults)

        let page = try await manager.encryptedEvents(afterSequence: 41)

        XCTAssertEqual(page.nextSequence, 42)
        XCTAssertEqual(page.events.first?.id, eventID)
        XCTAssertEqual(page.events.first?.deviceUUID, deviceID)
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
            XCTAssertEqual(request.url?.path, "/v1/app-installations")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            XCTAssertEqual(
                (object["installation_id"] as? String).flatMap(UUID.init(uuidString:)),
                installationID
            )
            XCTAssertEqual(object["identity_public_key"] as? String, publicKey.base64EncodedString())
            XCTAssertEqual(
                object["key_agreement_public_key"] as? String,
                keyAgreementPublicKey.base64EncodedString()
            )
            XCTAssertEqual(object["wrapped_vault_key"] as? String, wrappedKey.base64EncodedString())
            return Self.response(
                status: 201,
                body: """
                {"installation":{
                  "installation_id":"\(installationID.uuidString.lowercased())",
                  "identity_public_key":"\(publicKey.base64EncodedString())",
                  "key_agreement_public_key":"\(keyAgreementPublicKey.base64EncodedString())",
                  "wrapped_vault_key":"\(wrappedKey.base64EncodedString())",
                  "approved_at":"2026-10-16T13:00:00Z",
                  "created_at":"2026-10-16T13:00:00Z"
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
                "/v1/incoming-calls/\(notificationID.uuidString.lowercased())/claim"
            )
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            XCTAssertEqual(
                (object["installation_id"] as? String).flatMap(UUID.init(uuidString:)),
                installationID
            )
            return Self.response(
                status: 200,
                body: """
                {"claim":{
                  "notification_id":"\(notificationID.uuidString.lowercased())",
                  "installation_id":"\(installationID.uuidString.lowercased())",
                  "claimed_at":"2026-10-16T13:00:00Z",
                  "won":true
                }}
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
            XCTAssertEqual(error as? ServerConnectionError, .requestFailed(404))
        }
    }

    func testRegistersVoIPPushTokenForInstallation() async throws {
        let store = MemoryServerSessionStore(
            value: Data(Self.sessionJSON(accessToken: "access", refreshToken: "refresh").utf8)
        )
        let installationID = UUID(uuidString: "01900000-0000-7000-8000-000000000101")!
        let token = Data((0..<32).map(UInt8.init))
        let network = ScriptedServerNetwork { request in
            XCTAssertEqual(request.httpMethod, "PUT")
            XCTAssertEqual(
                request.url?.path,
                "/v1/app-installations/\(installationID.uuidString.lowercased())/push-tokens/voip"
            )
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: request.httpBody ?? Data()) as? [String: Any]
            )
            XCTAssertEqual(object["environment"] as? String, "sandbox")
            XCTAssertEqual(object["token"] as? String, token.base64EncodedString())
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
            XCTAssertEqual(request.httpMethod, "DELETE")
            XCTAssertEqual(
                request.url?.path,
                "/v1/app-installations/\(installationID.uuidString.lowercased())/push-tokens/voip"
            )
            XCTAssertEqual(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)?
                    .queryItems,
                [URLQueryItem(name: "environment", value: "sandbox")]
            )
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
private final class ScriptedServerNetwork: ServerNetworking {
    private(set) var requests: [URLRequest] = []
    private let handler: (URLRequest) throws -> (Data, HTTPURLResponse)

    init(handler: @escaping (URLRequest) throws -> (Data, HTTPURLResponse)) {
        self.handler = handler
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requests.append(request)
        return try handler(request)
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
