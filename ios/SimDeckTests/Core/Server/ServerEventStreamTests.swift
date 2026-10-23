// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class ServerEventStreamTests: XCTestCase {
    func testOpensAuthenticatedConnectStreamAndAcknowledgesDeliveredCursor() async throws {
        let eventStream = MonitorEventStream(pages: [Self.eventPage(sequence: 42)])
        let client = RecordingCommunicationSyncClient(stream: eventStream)
        let factory = RecordingCommunicationSyncClientFactory(client: client)
        let defaults = isolatedDefaults()
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let manager = ServerConnectionManager(
            network: FailingServerNetwork(),
            sessionStore: EventStreamSessionStore(value: Self.sessionData),
            defaults: defaults,
            communicationSyncClientFactory: factory
        )

        let stream = try await manager.openEncryptedEventStream(afterSequence: 41)
        let page = try await stream.receive()
        try await stream.acknowledge(sequence: page.nextSequence)

        XCTAssertEqual(factory.baseURL?.absoluteString, "https://deck.example.test")
        XCTAssertEqual(client.openedSequences, [41])
        XCTAssertEqual(client.accessTokens, ["access"])
        XCTAssertEqual(page.events.map(\.sequence), [42])
        XCTAssertEqual(eventStream.acknowledgedSequences, [42])
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

    func testMessageMonitorSynchronizesBeforeAcknowledgingAndStartsOnlyOnce() async {
        let synchronized = expectation(description: "message history synchronized")
        let acknowledged = expectation(description: "event cursor acknowledged")
        let stream = MonitorEventStream(
            pages: [Self.eventPage(sequence: 42)],
            onAcknowledge: { sequence in
                XCTAssertEqual(sequence, 42)
                acknowledged.fulfill()
            }
        )
        let opener = MonitorEventStreamOpener { sequence in
            XCTAssertEqual(sequence, 41)
            return stream
        }
        var cursor: Int64 = 41
        let monitor = MessageEventMonitor(
            opener: opener,
            currentSequence: { cursor },
            synchronizeThrough: { requiredSequence in
                XCTAssertEqual(requiredSequence, 42)
                cursor = requiredSequence
                synchronized.fulfill()
                return true
            }
        )

        monitor.start()
        monitor.start()
        await fulfillment(of: [synchronized, acknowledged], timeout: 1)
        monitor.stop()

        XCTAssertEqual(opener.openedSequences, [41])
        XCTAssertEqual(stream.acknowledgedSequences, [42])
        XCTAssertTrue(stream.didClose)
        XCTAssertFalse(monitor.isRunning)
    }

    func testMessageMonitorDoesNotAcknowledgeUnpersistedEvents() async {
        let reconnectAttempted = expectation(description: "reconnect attempted")
        let stream = MonitorEventStream(pages: [Self.eventPage(sequence: 42)])
        let opener = MonitorEventStreamOpener { _ in stream }
        let monitor = MessageEventMonitor(
            opener: opener,
            currentSequence: { 41 },
            synchronizeThrough: { _ in false },
            waitBeforeReconnect: {
                reconnectAttempted.fulfill()
                throw CancellationError()
            }
        )

        monitor.start()
        await fulfillment(of: [reconnectAttempted], timeout: 1)
        monitor.stop()

        XCTAssertTrue(stream.acknowledgedSequences.isEmpty)
        XCTAssertTrue(stream.didClose)
    }

    func testMessageMonitorReconnectsWhenTheSocketReportsCancellation() async {
        let acknowledged = expectation(description: "reconnected stream acknowledged")
        let stream = MonitorEventStream(
            pages: [Self.eventPage(sequence: 42)],
            onAcknowledge: { _ in acknowledged.fulfill() }
        )
        var attempt = 0
        let opener = MonitorEventStreamOpener { _ in
            attempt += 1
            if attempt == 1 {
                throw CancellationError()
            }
            return stream
        }
        var cursor: Int64 = 41
        let monitor = MessageEventMonitor(
            opener: opener,
            currentSequence: { cursor },
            synchronizeThrough: { requiredSequence in
                cursor = requiredSequence
                return true
            },
            waitBeforeReconnect: {}
        )

        monitor.start()
        await fulfillment(of: [acknowledged], timeout: 1)
        monitor.stop()

        XCTAssertEqual(opener.openedSequences, [41, 41])
        XCTAssertEqual(stream.acknowledgedSequences, [42])
    }

    func testNetworkSwitchClosesOldStreamAndResumesFromPersistedCursor() async {
        let firstAcknowledged = expectation(description: "first event acknowledged")
        let secondOpened = expectation(description: "stream reopened on new path")
        let first = MonitorEventStream(
            pages: [Self.eventPage(sequence: 42)],
            onAcknowledge: { _ in firstAcknowledged.fulfill() }
        )
        let second = MonitorEventStream(pages: [])
        let opener = MonitorEventStreamOpener { sequence in
            if sequence == 42 {
                secondOpened.fulfill()
                return second
            }
            XCTAssertEqual(sequence, 41)
            return first
        }
        var cursor: Int64 = 41
        let monitor = MessageEventMonitor(
            opener: opener,
            currentSequence: { cursor },
            synchronizeThrough: { sequence in
                cursor = sequence
                return true
            }
        )

        monitor.start()
        await fulfillment(of: [firstAcknowledged], timeout: 1)
        monitor.networkPathDidChange(isAvailable: false)
        XCTAssertTrue(first.didClose)
        XCTAssertFalse(monitor.isRunning)
        XCTAssertEqual(opener.openedSequences, [41])

        monitor.networkPathDidChange(isAvailable: true)
        await fulfillment(of: [secondOpened], timeout: 1)
        monitor.stop()

        XCTAssertEqual(opener.openedSequences, [41, 42])
        XCTAssertTrue(second.didClose)
    }

    func testLateOldConnectionDoesNotReplaceReopenedStream() async {
        let firstOpening = expectation(description: "first connection opening")
        let secondOpened = expectation(description: "second connection opened")
        let staleClosed = expectation(description: "stale connection closed")
        let stale = MonitorEventStream(pages: [], onClose: { staleClosed.fulfill() })
        let current = MonitorEventStream(pages: [])
        var pendingOpen: CheckedContinuation<any EncryptedEventStream, Never>?
        var attempts = 0
        let opener = MonitorEventStreamOpener { _ in
            attempts += 1
            if attempts == 1 {
                return await withCheckedContinuation { continuation in
                    pendingOpen = continuation
                    firstOpening.fulfill()
                }
            }
            secondOpened.fulfill()
            return current
        }
        let monitor = MessageEventMonitor(
            opener: opener,
            currentSequence: { 41 },
            synchronizeThrough: { _ in true }
        )

        monitor.start()
        await fulfillment(of: [firstOpening], timeout: 1)
        monitor.networkPathDidChange(isAvailable: false)
        monitor.networkPathDidChange(isAvailable: true)
        await fulfillment(of: [secondOpened], timeout: 1)
        pendingOpen?.resume(returning: stale)
        await fulfillment(of: [staleClosed], timeout: 1)

        XCTAssertFalse(current.didClose)
        XCTAssertTrue(monitor.isRunning)
        monitor.stop()
        XCTAssertTrue(current.didClose)
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

    private static func eventPage(sequence: Int64) -> EncryptedEventPage {
        EncryptedEventPage(
            events: [
                EncryptedEventEnvelope(
                    id: UUID(uuidString: "01900000-0000-7000-8000-000000000011")!,
                    sequence: sequence,
                    type: "message.received",
                    deviceUUID: UUID(uuidString: "01900000-0000-7000-8000-000000000001")!,
                    occurredAt: Date(timeIntervalSince1970: 1_792_109_000),
                    keyEpoch: 1,
                    nonce: Data(repeating: 1, count: 12),
                    ciphertext: Data(repeating: 2, count: 17),
                    signature: Data(repeating: 3, count: 64)
                ),
            ],
            nextSequence: sequence
        )
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
private final class MonitorEventStreamOpener: EncryptedEventStreamOpening {
    private let handler: @MainActor (Int64) async throws -> any EncryptedEventStream
    private(set) var openedSequences: [Int64] = []

    init(handler: @escaping @MainActor (Int64) async throws -> any EncryptedEventStream) {
        self.handler = handler
    }

    func openEncryptedEventStream(afterSequence: Int64) async throws -> any EncryptedEventStream {
        openedSequences.append(afterSequence)
        return try await handler(afterSequence)
    }

}

@MainActor
private final class RecordingCommunicationSyncClient: CommunicationSyncClient {
    private let eventStream: any EncryptedEventStream
    private(set) var openedSequences: [Int64] = []
    private(set) var accessTokens: [String] = []

    init(stream: any EncryptedEventStream) {
        eventStream = stream
    }

    func pullEvents(afterSequence: Int64, accessToken: String) async throws -> EncryptedEventPage {
        openedSequences.append(afterSequence)
        accessTokens.append(accessToken)
        return EncryptedEventPage(events: [], nextSequence: afterSequence)
    }

    func watchEvents(afterSequence: Int64, accessToken: String) throws -> any EncryptedEventStream {
        openedSequences.append(afterSequence)
        accessTokens.append(accessToken)
        return eventStream
    }
}

@MainActor
private final class RecordingCommunicationSyncClientFactory: CommunicationSyncClientCreating {
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
private final class MonitorEventStream: EncryptedEventStream {
    private var pages: [EncryptedEventPage]
    private let onAcknowledge: (Int64) -> Void
    private let onClose: () -> Void
    private(set) var acknowledgedSequences: [Int64] = []
    private(set) var didClose = false

    init(
        pages: [EncryptedEventPage],
        onAcknowledge: @escaping (Int64) -> Void = { _ in },
        onClose: @escaping () -> Void = {}
    ) {
        self.pages = pages
        self.onAcknowledge = onAcknowledge
        self.onClose = onClose
    }

    func receive() async throws -> EncryptedEventPage {
        if !pages.isEmpty {
            return pages.removeFirst()
        }
        try await Task.sleep(for: .seconds(60))
        throw CancellationError()
    }

    func acknowledge(sequence: Int64) async throws {
        acknowledgedSequences.append(sequence)
        onAcknowledge(sequence)
    }

    func close() {
        didClose = true
        onClose()
    }
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
