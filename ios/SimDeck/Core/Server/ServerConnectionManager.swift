import Foundation
import Observation
import Security

struct EncryptedCallCommandRequest: Encodable, Equatable, Sendable {
    let id: UUID
    let senderPublicKey: Data
    let nonce: Data
    let ciphertext: Data
    let createdAt: Date
    let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case id
        case senderPublicKey = "sender_public_key"
        case nonce
        case ciphertext
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }
}

struct EncryptedCallCommandResult: Decodable, Equatable, Sendable {
    let nonce: Data
    let ciphertext: Data
}

struct EncryptedCallCommandStatus: Decodable, Equatable, Sendable {
    let id: UUID
    let deviceUUID: UUID
    let state: String
    let result: EncryptedCallCommandResult?

    enum CodingKeys: String, CodingKey {
        case id
        case deviceUUID = "device_uuid"
        case state
        case result
    }
}

@MainActor
protocol EncryptedCallCommandAPI: AnyObject {
    func commandRecipient(deviceID: UUID) async throws -> Data
    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws
    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus
}

enum ServerConnectionState: Equatable, Sendable {
    case disconnected
    case connecting
    case connected
    case failed
}

enum ServerConnectionError: LocalizedError, Equatable {
    case invalidAddress
    case authenticationRequired
    case invalidResponse
    case requestFailed(Int)
    case deviceUnavailable

    var errorDescription: String? {
        switch self {
        case .invalidAddress:
            String(localized: "Enter a valid HTTPS server address.")
        case .authenticationRequired:
            String(localized: "Sign in to the server first.")
        case .invalidResponse:
            String(localized: "The server returned an invalid response.")
        case .requestFailed:
            String(localized: "The server request failed.")
        case .deviceUnavailable:
            String(localized: "The selected SIM cannot receive encrypted commands.")
        }
    }
}

struct ServerSession: Codable, Equatable, Sendable {
    let accessToken: String
    let refreshToken: String
    let accessExpiresAt: Date
    let refreshExpiresAt: Date

    enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case refreshToken = "refresh_token"
        case accessExpiresAt = "access_expires_at"
        case refreshExpiresAt = "refresh_expires_at"
    }
}

@MainActor
protocol ServerNetworking: Sendable {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

struct URLSessionServerNetworking: ServerNetworking {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let response = response as? HTTPURLResponse else {
            throw ServerConnectionError.invalidResponse
        }
        return (data, response)
    }
}

@MainActor
@Observable
final class ServerConnectionManager {
    var address: String
    private(set) var state: ServerConnectionState
    private(set) var lastErrorDescription: String?

    @ObservationIgnored private let network: any ServerNetworking
    @ObservationIgnored private let sessionStore: any SecureValueStoring
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private var session: ServerSession?

    private static let addressKey = "sim-deck.server-address.v1"

    init(
        network: any ServerNetworking,
        sessionStore: any SecureValueStoring,
        defaults: UserDefaults
    ) {
        self.network = network
        self.sessionStore = sessionStore
        self.defaults = defaults
        address = defaults.string(forKey: Self.addressKey) ?? ""
        session = try? sessionStore.read().map { data in
            try Self.decoder.decode(ServerSession.self, from: data)
        }
        state = session == nil ? .disconnected : .connected
    }

    static func live() -> ServerConnectionManager {
        ServerConnectionManager(
            network: URLSessionServerNetworking(),
            sessionStore: KeychainValueStore(
                service: "com.orangeboychen.simdeck.server",
                account: "session-v1",
                synchronizable: false,
                accessibility: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String
            ),
            defaults: .standard
        )
    }

    var isSignedIn: Bool { session != nil }

    func signIn(password: String) async throws {
        state = .connecting
        lastErrorDescription = nil
        do {
            let baseURL = try validatedBaseURL()
            var request = URLRequest(url: baseURL.appending(path: "v1/auth/bootstrap"))
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(BootstrapRequest(username: "admin", password: password))
            let (data, response) = try await network.data(for: request)
            guard response.statusCode == 201 else {
                throw ServerConnectionError.requestFailed(response.statusCode)
            }
            let session = try Self.decoder.decode(ServerSession.self, from: data)
            try save(session)
            defaults.set(address.trimmingCharacters(in: .whitespacesAndNewlines), forKey: Self.addressKey)
            state = .connected
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            throw error
        }
    }

    func signOut() throws {
        try sessionStore.delete()
        session = nil
        state = .disconnected
        lastErrorDescription = nil
    }

    func loadDevices(now: Date = .now) async throws -> [DeviceLineIdentity] {
        do {
            let (data, response) = try await authenticatedRequest(path: "v1/devices", method: "GET")
            guard response.statusCode == 200 else {
                throw ServerConnectionError.requestFailed(response.statusCode)
            }
            let body = try Self.decoder.decode(DeviceListResponse.self, from: data)
            let devices = body.devices.map { device in
                let recentlySeen = device.lastSeenAt.map {
                    let age = now.timeIntervalSince($0)
                    return age >= 0 && age < 60
                } ?? false
                return DeviceLineIdentity(
                    id: device.deviceUUID,
                    numericID: device.numericID,
                    deviceName: "",
                    lineName: "",
                    phoneNumber: "",
                    isOnline: recentlySeen && device.commandPublicKey?.count == 32,
                    commandPublicKey: device.commandPublicKey
                )
            }
            state = .connected
            lastErrorDescription = nil
            return devices
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            throw error
        }
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        guard let recipient = try await loadDevices().first(where: { $0.id == deviceID }),
              recipient.isOnline,
              let publicKey = recipient.commandPublicKey,
              publicKey.count == 32 else {
            throw ServerConnectionError.deviceUnavailable
        }
        return publicKey
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        let body = try Self.encoder.encode(command)
        let (_, response) = try await authenticatedRequest(
            path: "v1/devices/\(deviceID.uuidString.lowercased())/commands",
            method: "POST",
            body: body
        )
        guard response.statusCode == 200 || response.statusCode == 201 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus {
        let (data, response) = try await authenticatedRequest(
            path: "v1/devices/\(deviceID.uuidString.lowercased())/commands/\(commandID.uuidString.lowercased())",
            method: "GET"
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(CommandStatusResponse.self, from: data).command
    }

    private func authenticatedRequest(
        path: String,
        method: String,
        body: Data? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        let baseURL = try validatedBaseURL()
        var activeSession = try await validSession(baseURL: baseURL)
        var urlRequest = request(
            baseURL: baseURL,
            path: path,
            method: method,
            body: body,
            accessToken: activeSession.accessToken
        )
        var result = try await network.data(for: urlRequest)
        if result.1.statusCode == 401 {
            activeSession = try await refresh(baseURL: baseURL, session: activeSession)
            urlRequest = request(
                baseURL: baseURL,
                path: path,
                method: method,
                body: body,
                accessToken: activeSession.accessToken
            )
            result = try await network.data(for: urlRequest)
        }
        return result
    }

    private func validSession(baseURL: URL) async throws -> ServerSession {
        guard let session else { throw ServerConnectionError.authenticationRequired }
        if session.accessExpiresAt > Date().addingTimeInterval(15) {
            return session
        }
        return try await refresh(baseURL: baseURL, session: session)
    }

    private func refresh(baseURL: URL, session: ServerSession) async throws -> ServerSession {
        guard session.refreshExpiresAt > .now else {
            throw ServerConnectionError.authenticationRequired
        }
        var request = URLRequest(url: baseURL.appending(path: "v1/auth/refresh"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(RefreshRequest(refreshToken: session.refreshToken))
        let (data, response) = try await network.data(for: request)
        guard response.statusCode == 200 else {
            throw ServerConnectionError.authenticationRequired
        }
        let replacement = try Self.decoder.decode(ServerSession.self, from: data)
        try save(replacement)
        return replacement
    }

    private func save(_ session: ServerSession) throws {
        try sessionStore.write(Self.encoder.encode(session))
        self.session = session
    }

    private func validatedBaseURL() throws -> URL {
        guard let url = URL(string: address.trimmingCharacters(in: .whitespacesAndNewlines)),
              url.scheme == "https", url.host != nil else {
            throw ServerConnectionError.invalidAddress
        }
        return url
    }

    private func request(baseURL: URL, path: String, method: String, body: Data?, accessToken: String) -> URLRequest {
        var request = URLRequest(url: baseURL.appending(path: path))
        request.httpMethod = method
        request.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return request
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
}

extension ServerConnectionManager: EncryptedCallCommandAPI {}

private struct BootstrapRequest: Encodable {
    let username: String
    let password: String
}

private struct RefreshRequest: Encodable {
    let refreshToken: String

    enum CodingKeys: String, CodingKey {
        case refreshToken = "refresh_token"
    }
}

private struct DeviceListResponse: Decodable {
    let devices: [ServerDeviceRecord]
}

private struct ServerDeviceRecord: Decodable {
    let deviceUUID: UUID
    let numericID: Int
    let commandPublicKey: Data?
    let lastSeenAt: Date?

    enum CodingKeys: String, CodingKey {
        case deviceUUID = "device_uuid"
        case numericID = "numeric_id"
        case commandPublicKey = "command_public_key"
        case lastSeenAt = "last_seen_at"
    }
}

private struct CommandStatusResponse: Decodable {
    let command: EncryptedCallCommandStatus
}
