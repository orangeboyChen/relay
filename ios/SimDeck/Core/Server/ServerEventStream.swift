// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import OSLog
import Connect
import RelayProtocol

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

@MainActor
final class ServerConnectEncryptedEventStream: EncryptedEventStream {
    private let stream: any Connect.ServerOnlyAsyncStreamInterface<
        Relay_Communication_V1_WatchEventsRequest,
        Relay_Communication_V1_WatchEventsResponse
    >
    private var results: AsyncStream<Connect.StreamResult<Relay_Communication_V1_WatchEventsResponse>>.Iterator
    private var cursor: Int64
    private var pendingAcknowledgement: Int64?

    init(
        stream: any Connect.ServerOnlyAsyncStreamInterface<
            Relay_Communication_V1_WatchEventsRequest,
            Relay_Communication_V1_WatchEventsResponse
        >,
        afterSequence: Int64
    ) throws {
        guard afterSequence >= 0 else {
            throw ServerConnectionError.invalidResponse
        }
        self.stream = stream
        self.cursor = afterSequence
        self.results = stream.results().makeAsyncIterator()
        try stream.send(Relay_Communication_V1_WatchEventsRequest.with {
            $0.afterSequence = afterSequence
        })
    }

    func receive() async throws -> EncryptedEventPage {
        guard pendingAcknowledgement == nil else {
            throw ServerConnectionError.invalidResponse
        }
        while let result = await results.next() {
            switch result {
            case .headers:
                continue
            case .complete(_, let error, _):
                if let error { throw error }
                throw ServerConnectionError.eventStreamUnavailable
            case .message(let response):
                let events = try response.events.map(ServerConnectionManager.eventEnvelope(from:))
                guard !events.isEmpty,
                      response.nextSequence > cursor,
                      events.last?.sequence == response.nextSequence else {
                    throw ServerConnectionError.invalidResponse
                }
                var previousSequence = cursor
                for event in events {
                    guard event.sequence > previousSequence else {
                        throw ServerConnectionError.invalidResponse
                    }
                    previousSequence = event.sequence
                }
                pendingAcknowledgement = response.nextSequence
                return EncryptedEventPage(events: events, nextSequence: response.nextSequence)
            }
        }
        throw ServerConnectionError.eventStreamUnavailable
    }

    func acknowledge(sequence: Int64) async throws {
        guard pendingAcknowledgement == sequence else {
            throw ServerConnectionError.invalidResponse
        }
        cursor = sequence
        pendingAcknowledgement = nil
    }

    func close() {
        stream.cancel()
    }
}

@MainActor
final class MessageEventMonitor {
    private static let logger = Logger(
        subsystem: Bundle.main.bundleIdentifier ?? "SimDeck",
        category: "MessageEventMonitor"
    )

    private let opener: any EncryptedEventStreamOpening
    private let currentSequence: () -> Int64
    private let synchronizeThrough: (Int64) async -> Bool
    private let onDisconnected: () -> Void
    private let waitBeforeReconnect: () async throws -> Void
    private var task: Task<Void, Never>?
    private var activeStream: (any EncryptedEventStream)?
    private var streamGeneration: UUID?
    private var wantsUpdates = false
    private var networkIsAvailable = true

    init(
        opener: any EncryptedEventStreamOpening,
        currentSequence: @escaping () -> Int64,
        synchronizeThrough: @escaping (Int64) async -> Bool,
        onDisconnected: @escaping () -> Void = {},
        waitBeforeReconnect: @escaping () async throws -> Void = {
            try await Task.sleep(for: .seconds(2))
        }
    ) {
        self.opener = opener
        self.currentSequence = currentSequence
        self.synchronizeThrough = synchronizeThrough
        self.onDisconnected = onDisconnected
        self.waitBeforeReconnect = waitBeforeReconnect
    }

    var isRunning: Bool { task != nil }

    func start() {
        wantsUpdates = true
        guard networkIsAvailable else { return }
        guard task == nil else { return }
        let generation = UUID()
        streamGeneration = generation
        task = Task { [weak self] in
            await self?.run(generation: generation)
        }
    }

    func stop() {
        wantsUpdates = false
        cancelActiveStream()
    }

    func networkPathDidChange(isAvailable: Bool) {
        networkIsAvailable = isAvailable
        guard wantsUpdates else { return }
        cancelActiveStream()
        if isAvailable {
            start()
        }
    }

    private func cancelActiveStream() {
        streamGeneration = nil
        task?.cancel()
        task = nil
        activeStream?.close()
        activeStream = nil
    }

    private func run(generation: UUID) async {
        defer {
            if streamGeneration == generation {
                streamGeneration = nil
                task = nil
            }
        }
        while streamGeneration == generation && !Task.isCancelled {
            do {
                let sequence = currentSequence()
                Self.logger.info("Opening encrypted message event stream after sequence \(sequence, privacy: .public)")
                let stream = try await opener.openEncryptedEventStream(afterSequence: sequence)
                guard streamGeneration == generation, !Task.isCancelled else {
                    stream.close()
                    return
                }
                activeStream = stream
                defer {
                    stream.close()
                    if activeStream === stream {
                        activeStream = nil
                    }
                }

                while streamGeneration == generation && !Task.isCancelled {
                    let page = try await stream.receive()
                    guard streamGeneration == generation, !Task.isCancelled else { return }
                    let synchronized = await synchronizeThrough(page.nextSequence)
                    guard streamGeneration == generation, !Task.isCancelled else { return }
                    guard synchronized else {
                        throw ServerConnectionError.eventStreamUnavailable
                    }
                    try await stream.acknowledge(sequence: page.nextSequence)
                    Self.logger.debug(
                        "Acknowledged encrypted message events through sequence \(page.nextSequence, privacy: .public)"
                    )
                }
            } catch {
                guard streamGeneration == generation, !Task.isCancelled else { return }
                Self.logger.error(
                    "Encrypted message event stream disconnected: \(String(reflecting: error), privacy: .public)"
                )
                onDisconnected()
                do {
                    try await waitBeforeReconnect()
                } catch {
                    return
                }
            }
        }
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
