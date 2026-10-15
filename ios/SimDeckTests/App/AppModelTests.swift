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
            isOnline: true
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

        try model.signOutFromServer()

        XCTAssertEqual(connection.state, .disconnected)
        XCTAssertTrue(model.devices.isEmpty)
        XCTAssertNil(sessionStore.storedValue)
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
