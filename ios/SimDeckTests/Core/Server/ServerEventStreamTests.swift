import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class ServerEventStreamTests: XCTestCase {
    func testOpensAuthenticatedWSSStreamAndAcknowledgesDeliveredCursor() async throws {
        let connection = RecordingServerWebSocketConnection(incoming: [
            .string(Self.eventFrame(sequence: 42)),
        ])
        let factory = RecordingServerWebSocketFactory(connection: connection)
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: FailingServerNetwork(),
            sessionStore: EventStreamSessionStore(value: Self.sessionData),
            defaults: defaults,
            webSocketFactory: factory
        )

        let stream = try await manager.openEncryptedEventStream(afterSequence: 41)
        let page = try await stream.receive()
        try await stream.acknowledge(sequence: page.nextSequence)

        let request = try XCTUnwrap(factory.request)
        XCTAssertEqual(request.url?.scheme, "wss")
        XCTAssertEqual(request.url?.host, "deck.example.test")
        XCTAssertEqual(request.url?.path, "/v1/events/ws")
        XCTAssertEqual(request.url?.query, "after_sequence=41")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access")
        XCTAssertTrue(connection.didResume)
        XCTAssertEqual(page.events.map(\.sequence), [42])
        let acknowledgement = try XCTUnwrap(connection.sent.first?.dataValue)
        XCTAssertEqual(
            try JSONSerialization.jsonObject(with: acknowledgement) as? [String: AnyHashable],
            ["type": "ack", "sequence": 42]
        )
    }

    func testRejectsNonAdvancingEventSequenceWithoutAcknowledgingIt() async throws {
        let connection = RecordingServerWebSocketConnection(incoming: [
            .string(Self.eventFrame(sequence: 41)),
        ])
        let stream = ServerEncryptedEventStream(
            connection: connection,
            afterSequence: 41,
            decoder: Self.decoder,
            encoder: JSONEncoder()
        )

        do {
            _ = try await stream.receive()
            XCTFail("Expected the stale event cursor to be rejected")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .invalidResponse)
        }
        XCTAssertTrue(connection.sent.isEmpty)
    }

    func testMapsSanitizedServerStreamErrors() async throws {
        let connection = RecordingServerWebSocketConnection(incoming: [
            .string(#"{"type":"error","code":"storage_error"}"#),
        ])
        let stream = ServerEncryptedEventStream(
            connection: connection,
            afterSequence: 0,
            decoder: Self.decoder,
            encoder: JSONEncoder()
        )

        do {
            _ = try await stream.receive()
            XCTFail("Expected the server stream error")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .eventStreamUnavailable)
        }
    }

    private func isolatedDefaults() -> UserDefaults {
        let suiteName = "ServerEventStreamTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defaults.removePersistentDomain(forName: suiteName)
        return defaults
    }

    private static func eventFrame(sequence: Int64) -> String {
        let nonce = Data(repeating: 1, count: 12).base64EncodedString()
        let ciphertext = Data(repeating: 2, count: 17).base64EncodedString()
        let signature = Data(repeating: 3, count: 64).base64EncodedString()
        return """
        {"type":"events","events":[{
          "id":"01900000-0000-7000-8000-000000000011",
          "sequence":\(sequence),
          "type":"call.state",
          "device_uuid":"01900000-0000-7000-8000-000000000001",
          "occurred_at":"2026-10-16T00:00:00Z",
          "key_epoch":1,
          "nonce":"\(nonce)",
          "ciphertext":"\(ciphertext)",
          "signature":"\(signature)"
        }],"next_sequence":\(sequence)}
        """
    }

    private static let sessionData = Data(
        #"{"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}"#.utf8
    )

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
}

@MainActor
private final class RecordingServerWebSocketFactory: ServerWebSocketCreating {
    private let connection: any ServerWebSocketConnection
    private(set) var request: URLRequest?

    init(connection: any ServerWebSocketConnection) {
        self.connection = connection
    }

    func makeConnection(for request: URLRequest) -> any ServerWebSocketConnection {
        self.request = request
        return connection
    }
}

@MainActor
private final class RecordingServerWebSocketConnection: ServerWebSocketConnection {
    private var incoming: [ServerWebSocketMessage]
    private(set) var sent: [ServerWebSocketMessage] = []
    private(set) var didResume = false
    private(set) var didCancel = false

    init(incoming: [ServerWebSocketMessage]) {
        self.incoming = incoming
    }

    func resume() {
        didResume = true
    }

    func receive() async throws -> ServerWebSocketMessage {
        guard !incoming.isEmpty else {
            throw ServerConnectionError.eventStreamUnavailable
        }
        return incoming.removeFirst()
    }

    func send(_ message: ServerWebSocketMessage) async throws {
        sent.append(message)
    }

    func cancel() {
        didCancel = true
    }
}

private extension ServerWebSocketMessage {
    var dataValue: Data? {
        guard case .data(let data) = self else { return nil }
        return data
    }
}

@MainActor
private struct FailingServerNetwork: ServerNetworking {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        throw ServerConnectionError.invalidResponse
    }
}

private final class EventStreamSessionStore: SecureValueStoring, @unchecked Sendable {
    private var value: Data?

    init(value: Data?) {
        self.value = value
    }

    func read() throws -> Data? { value }
    func write(_ data: Data) throws { value = data }
    func delete() throws { value = nil }
}
