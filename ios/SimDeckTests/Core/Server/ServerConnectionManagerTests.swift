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
                      "last_seen_at":"2026-10-16T00:00:30Z"
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
        let loadedIdentityKey = try await manager.eventVerificationKey(deviceID: deviceID)
        XCTAssertEqual(loadedIdentityKey, identityKey)
        XCTAssertTrue(devices[0].isOnline)
        XCTAssertFalse(devices[1].isOnline)
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
