import Foundation

enum ServerWebSocketMessage: Equatable, Sendable {
    case data(Data)
    case string(String)
}

@MainActor
protocol ServerWebSocketConnection: AnyObject {
    func resume()
    func receive() async throws -> ServerWebSocketMessage
    func send(_ message: ServerWebSocketMessage) async throws
    func cancel()
}

@MainActor
protocol ServerWebSocketCreating: AnyObject {
    func makeConnection(for request: URLRequest) -> any ServerWebSocketConnection
}

@MainActor
protocol EncryptedEventStream: AnyObject {
    func receive() async throws -> EncryptedEventPage
    func acknowledge(sequence: Int64) async throws
    func close()
}

@MainActor
protocol EncryptedEventStreamOpening: AnyObject {
    func openEncryptedEventStream(afterSequence: Int64) async throws -> any EncryptedEventStream
}

@MainActor
final class URLSessionServerWebSocketFactory: ServerWebSocketCreating {
    private let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    func makeConnection(for request: URLRequest) -> any ServerWebSocketConnection {
        URLSessionServerWebSocketConnection(task: session.webSocketTask(with: request))
    }
}

@MainActor
private final class URLSessionServerWebSocketConnection: ServerWebSocketConnection {
    private let task: URLSessionWebSocketTask

    init(task: URLSessionWebSocketTask) {
        self.task = task
    }

    func resume() {
        task.resume()
    }

    func receive() async throws -> ServerWebSocketMessage {
        switch try await task.receive() {
        case .data(let data):
            return .data(data)
        case .string(let string):
            return .string(string)
        @unknown default:
            throw ServerConnectionError.invalidResponse
        }
    }

    func send(_ message: ServerWebSocketMessage) async throws {
        switch message {
        case .data(let data):
            try await task.send(.data(data))
        case .string(let string):
            try await task.send(.string(string))
        }
    }

    func cancel() {
        task.cancel(with: .goingAway, reason: nil)
    }
}

@MainActor
final class ServerEncryptedEventStream: EncryptedEventStream {
    private let connection: any ServerWebSocketConnection
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder
    private var cursor: Int64
    private var pendingAcknowledgement: Int64?

    init(
        connection: any ServerWebSocketConnection,
        afterSequence: Int64,
        decoder: JSONDecoder,
        encoder: JSONEncoder
    ) {
        self.connection = connection
        self.cursor = afterSequence
        self.decoder = decoder
        self.encoder = encoder
    }

    func receive() async throws -> EncryptedEventPage {
        guard pendingAcknowledgement == nil else {
            throw ServerConnectionError.invalidResponse
        }
        let message = try await connection.receive()
        let data: Data
        switch message {
        case .data(let value):
            data = value
        case .string(let value):
            data = Data(value.utf8)
        }
        let frame = try decoder.decode(EventStreamFrame.self, from: data)
        if frame.type == "error" {
            throw ServerConnectionError.eventStreamUnavailable
        }
        guard frame.type == "events",
              let events = frame.events,
              let nextSequence = frame.nextSequence,
              !events.isEmpty,
              nextSequence > cursor,
              events.last?.sequence == nextSequence else {
            throw ServerConnectionError.invalidResponse
        }
        var previousSequence = cursor
        for event in events {
            guard event.sequence > previousSequence else {
                throw ServerConnectionError.invalidResponse
            }
            previousSequence = event.sequence
        }
        pendingAcknowledgement = nextSequence
        return EncryptedEventPage(events: events, nextSequence: nextSequence)
    }

    func acknowledge(sequence: Int64) async throws {
        guard pendingAcknowledgement == sequence else {
            throw ServerConnectionError.invalidResponse
        }
        let data = try encoder.encode(EventStreamAcknowledgement(sequence: sequence))
        try await connection.send(.data(data))
        cursor = sequence
        pendingAcknowledgement = nil
    }

    func close() {
        connection.cancel()
    }
}

private struct EventStreamFrame: Decodable {
    let type: String
    let events: [EncryptedEventEnvelope]?
    let nextSequence: Int64?

    enum CodingKeys: String, CodingKey {
        case type
        case events
        case nextSequence = "next_sequence"
    }
}

private struct EventStreamAcknowledgement: Encodable {
    let type = "ack"
    let sequence: Int64
}
