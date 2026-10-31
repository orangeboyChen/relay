// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import Observation
import OSLog
import Connect
import RelayProtocol
import Security
import SwiftProtobuf

struct EncryptedCallCommandRequest: Encodable, Equatable, Sendable {
    let id: UUID
    let senderInstallationID: UUID
    let senderPublicKey: Data
    let nonce: Data
    let ciphertext: Data
    let signature: Data
    let createdAt: Date
    let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case id
        case senderInstallationID = "sender_installation_id"
        case senderPublicKey = "sender_public_key"
        case nonce
        case ciphertext
        case signature
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

struct EncryptedEventEnvelope: Decodable, Equatable, Sendable {
    let id: UUID
    let sequence: Int64
    let type: String
    let deviceUUID: UUID
    let recipientInstallationID: UUID?
    let occurredAt: Date
    let keyEpoch: Int
    let nonce: Data
    let ciphertext: Data
    let signature: Data

    init(
        id: UUID,
        sequence: Int64,
        type: String,
        deviceUUID: UUID,
        recipientInstallationID: UUID? = nil,
        occurredAt: Date,
        keyEpoch: Int,
        nonce: Data,
        ciphertext: Data,
        signature: Data
    ) {
        self.id = id
        self.sequence = sequence
        self.type = type
        self.deviceUUID = deviceUUID
        self.recipientInstallationID = recipientInstallationID
        self.occurredAt = occurredAt
        self.keyEpoch = keyEpoch
        self.nonce = nonce
        self.ciphertext = ciphertext
        self.signature = signature
    }

    init(from decoder: Swift.Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        sequence = try values.decode(Int64.self, forKey: .sequence)
        type = try values.decode(String.self, forKey: .type)
        deviceUUID = try values.decode(UUID.self, forKey: .deviceUUID)
        recipientInstallationID = try values.decodeIfPresent(UUID.self, forKey: .recipientInstallationID)
        occurredAt = try values.decode(Date.self, forKey: .occurredAt)
        keyEpoch = try values.decode(Int.self, forKey: .keyEpoch)
        nonce = try values.decode(Data.self, forKey: .nonce)
        ciphertext = try values.decode(Data.self, forKey: .ciphertext)
        signature = try values.decode(Data.self, forKey: .signature)
    }

    enum CodingKeys: String, CodingKey {
        case id
        case sequence
        case type
        case deviceUUID = "device_uuid"
        case recipientInstallationID = "recipient_installation_id"
        case occurredAt = "occurred_at"
        case keyEpoch = "key_epoch"
        case nonce
        case ciphertext
        case signature
    }
}

struct EncryptedEventPage: Decodable, Equatable, Sendable {
    let events: [EncryptedEventEnvelope]
    let nextSequence: Int64

    enum CodingKeys: String, CodingKey {
        case events
        case nextSequence = "next_sequence"
    }
}

struct AppInstallationRegistration: Equatable, Sendable {
    let id: UUID
    let identityPublicKey: Data
    let keyAgreementPublicKey: Data
    let wrappedVaultKey: Data?
}

struct AppInstallationTrust: Decodable, Equatable, Sendable {
    let id: UUID
    let identityPublicKey: Data
    let keyAgreementPublicKey: Data
    let wrappedVaultKey: Data?
    let approvedAt: Date?
    let revokedAt: Date?
    let createdAt: Date

    var isApproved: Bool { approvedAt != nil && revokedAt == nil }

    enum CodingKeys: String, CodingKey {
        case id = "installation_id"
        case identityPublicKey = "identity_public_key"
        case keyAgreementPublicKey = "key_agreement_public_key"
        case wrappedVaultKey = "wrapped_vault_key"
        case approvedAt = "approved_at"
        case revokedAt = "revoked_at"
        case createdAt = "created_at"
    }
}

struct IncomingCallClaim: Decodable, Equatable, Sendable {
    let notificationID: UUID
    let installationID: UUID
    let claimedAt: Date
    let won: Bool

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case installationID = "installation_id"
        case claimedAt = "claimed_at"
        case won
    }
}

struct DeviceDiscoverySession: Decodable, Equatable, Sendable {
    let openedAt: Date
    let expiresAt: Date
    let closedAt: Date?

    enum CodingKeys: String, CodingKey {
        case openedAt = "opened_at"
        case expiresAt = "expires_at"
        case closedAt = "closed_at"
    }
}

struct DeviceDiscoverySessionState: Decodable, Equatable, Sendable {
    let active: Bool
    let session: DeviceDiscoverySession?
}

enum DeviceDiscoveryStatus: String, Decodable, Equatable, Sendable {
    case pending
    case approved
    case rejected
}

struct DiscoveredDevice: Decodable, Equatable, Identifiable, Sendable {
    let deviceUUID: UUID
    let identityPublicKey: Data
    let commandPublicKey: Data
    let status: DeviceDiscoveryStatus
    let firstSeenAt: Date
    let lastSeenAt: Date
    let expiresAt: Date

    var id: UUID { deviceUUID }

    enum CodingKeys: String, CodingKey {
        case deviceUUID = "device_uuid"
        case identityPublicKey = "identity_public_key"
        case commandPublicKey = "command_public_key"
        case status
        case firstSeenAt = "first_seen_at"
        case lastSeenAt = "last_seen_at"
        case expiresAt = "expires_at"
    }
}

struct ApprovedDevice: Decodable, Equatable, Sendable {
    let deviceUUID: UUID
    let numericID: Int

    enum CodingKeys: String, CodingKey {
        case deviceUUID = "device_uuid"
        case numericID = "numeric_id"
    }
}

@MainActor
protocol DeviceDiscoveryAPI: AnyObject {
    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState
    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState
    func closeDeviceDiscovery() async throws
    func discoveredDevices() async throws -> [DiscoveredDevice]
    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice
    func rejectDiscoveredDevice(id: UUID) async throws
}

@MainActor
protocol IncomingCallClaiming: AnyObject {
    func claimIncomingCall(notificationID: UUID, installationID: UUID) async throws
        -> IncomingCallClaim
}

@MainActor
protocol IncomingCallClaimReleasing: AnyObject {
    func releaseIncomingCall(notificationID: UUID, installationID: UUID) async throws
}

enum PushTokenKind: String, Sendable {
    case alert
    case voip
}

enum PushEnvironment: String, Sendable {
    case sandbox
    case production
}

@MainActor
protocol PushTokenRegistering: AnyObject {
    func registerPushToken(
        _ token: Data,
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws
    func unregisterPushToken(
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws
}

@MainActor
protocol EncryptedCallCommandAPI: AnyObject {
    func commandRecipient(deviceID: UUID) async throws -> Data
    func commandTarget(for line: DeviceLineIdentity) async throws -> EncryptedCommandTarget
    func eventVerificationKey(deviceID: UUID) async throws -> Data
    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws
    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus
    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage
}

struct EncryptedCommandTarget: Equatable, Sendable {
    let deviceID: UUID
    let slotID: UUID?
    let simID: UUID?
    let publicKey: Data
}

extension EncryptedCallCommandAPI {
    func commandTarget(for line: DeviceLineIdentity) async throws -> EncryptedCommandTarget {
        EncryptedCommandTarget(
            deviceID: line.deviceID,
            slotID: line.slotID,
            simID: line.simID,
            publicKey: try await commandRecipient(deviceID: line.deviceID)
        )
    }
}

@MainActor
protocol CallTURNCredentialProviding: AnyObject {
    func turnCredentials() async throws -> CallTURNCredentials
}

struct CallTURNCredentials: Decodable, Equatable, Sendable {
	let urls: [String]
	let username: String
	let credential: String
	let expiresAt: Date

	enum CodingKeys: String, CodingKey {
		case urls
		case username
		case credential
		case expiresAt = "expires_at"
	}
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
    case eventStreamUnavailable

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
        case .eventStreamUnavailable:
            String(localized: "The real-time event connection is unavailable.")
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

struct ManagedSIM: Identifiable, Equatable, Sendable {
    let id: UUID
    let numericID: Int
    let assignedDeviceID: UUID?
    let assignedSlotID: UUID?
    let lastSeenAt: Date
    let lineName: String
    let phoneNumber: String

    var isOffline: Bool { assignedDeviceID == nil }
}

@MainActor
protocol ServerNetworking: Sendable {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
    func ownerRPCClientFactory() -> (any OwnerRPCClientCreating)?
}

extension ServerNetworking {
    func ownerRPCClientFactory() -> (any OwnerRPCClientCreating)? { nil }
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
protocol CommunicationSyncClient: AnyObject {
    func pullEvents(afterSequence: Int64, accessToken: String) async throws -> EncryptedEventPage
    func watchEvents(afterSequence: Int64, accessToken: String) throws -> any EncryptedEventStream
    func getHistorySnapshot(kind: Relay_Communication_V1_HistoryKind, accessToken: String) async throws -> HistorySnapshotPayload
}

struct HistorySnapshotPayload: Equatable, Sendable {
    let revision: Int64
    let ciphertext: Data
}

@MainActor
protocol CommunicationSyncClientCreating: AnyObject {
    func makeClient(baseURL: URL) -> any CommunicationSyncClient
}

@MainActor
protocol OwnerRPCClient: AnyObject {
    var service: Relay_Communication_V1_OwnerServiceClientInterface { get }
}

@MainActor
protocol OwnerRPCClientCreating: AnyObject { func makeClient(baseURL: URL) -> any OwnerRPCClient }

@MainActor
private final class ConnectOwnerRPCClientFactory: OwnerRPCClientCreating {
    func makeClient(baseURL: URL) -> any OwnerRPCClient { ConnectOwnerRPCClient(baseURL: baseURL) }
}

@MainActor
private final class ConnectOwnerRPCClient: OwnerRPCClient {
    let service: Relay_Communication_V1_OwnerServiceClientInterface

    init(baseURL: URL) {
        let config = ProtocolClientConfig(host: baseURL.absoluteString, codec: ProtoCodec())
        service = Relay_Communication_V1_OwnerServiceClient(client: ProtocolClient(httpClient: URLSessionHTTPClient(), config: config))
    }
}

struct PasskeyOptionsPayload: Equatable, Sendable {
    let ceremonyID: String
    let optionsJSON: Data
}

struct PasskeySummary: Identifiable, Equatable, Sendable {
    let id: UUID
    let createdAt: Date?
    let lastUsedAt: Date?
}

@MainActor
protocol PasskeyRPCClient: AnyObject {
    func bootstrap(username: String, password: String) async throws -> ServerSession
    func refresh(refreshToken: String) async throws -> ServerSession
    func logout(accessToken: String) async throws
    func beginRegistration(accessToken: String) async throws -> PasskeyOptionsPayload
    func finishRegistration(ceremonyID: String, credentialJSON: Data, accessToken: String) async throws
    func listPasskeys(accessToken: String) async throws -> [PasskeySummary]
    func beginLogin() async throws -> PasskeyOptionsPayload
    func finishLogin(ceremonyID: String, credentialJSON: Data) async throws -> ServerSession
}

@MainActor
extension PasskeyRPCClient {
    func listPasskeys(accessToken: String) async throws -> [PasskeySummary] {
        []
    }
}

@MainActor
protocol PasskeyRPCClientCreating: AnyObject {
    func makeClient(baseURL: URL) -> any PasskeyRPCClient
}

@MainActor
private final class ConnectPasskeyRPCClientFactory: PasskeyRPCClientCreating {
    func makeClient(baseURL: URL) -> any PasskeyRPCClient {
        ConnectPasskeyRPCClient(baseURL: baseURL)
    }
}

@MainActor
private final class ConnectPasskeyRPCClient: PasskeyRPCClient {
    private let client: Relay_Communication_V1_AuthenticationServiceClient

    init(baseURL: URL) {
        let config = ProtocolClientConfig(host: baseURL.absoluteString, codec: ProtoCodec())
        client = Relay_Communication_V1_AuthenticationServiceClient(
            client: ProtocolClient(httpClient: URLSessionHTTPClient(), config: config)
        )
    }

    func bootstrap(username: String, password: String) async throws -> ServerSession {
        let response = await client.bootstrap(
            request: Relay_Communication_V1_BootstrapRequest.with {
                $0.username = username
                $0.password = password
            }
        )
        return Self.session(from: try response.result.get())
    }

    func refresh(refreshToken: String) async throws -> ServerSession {
        let response = await client.refresh(
            request: Relay_Communication_V1_RefreshRequest.with { $0.refreshToken = refreshToken }
        )
        return Self.session(from: try response.result.get())
    }

    func logout(accessToken: String) async throws {
        let response = await client.logout(
            request: Relay_Communication_V1_LogoutRequest(),
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        _ = try response.result.get()
    }

    func beginRegistration(accessToken: String) async throws -> PasskeyOptionsPayload {
        let response = await client.beginPasskeyRegistration(
            request: Relay_Communication_V1_BeginPasskeyRegistrationRequest(),
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        let result = try response.result.get()
        return PasskeyOptionsPayload(ceremonyID: result.ceremonyID, optionsJSON: result.optionsJson)
    }

    func finishRegistration(ceremonyID: String, credentialJSON: Data, accessToken: String) async throws {
        let response = await client.finishPasskeyRegistration(
            request: Relay_Communication_V1_FinishPasskeyRegistrationRequest.with {
                $0.ceremonyID = ceremonyID
                $0.credentialJson = credentialJSON
            },
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        _ = try response.result.get()
    }

    func listPasskeys(accessToken: String) async throws -> [PasskeySummary] {
        let response = await client.listPasskeys(
            request: Relay_Communication_V1_ListPasskeysRequest(),
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        let result = try response.result.get()
        return result.passkeys.enumerated().map { index, passkey in
            PasskeySummary(
                id: UUID(uuidString: "00000000-0000-0000-0000-\(String(format: "%012d", index + 1))") ?? UUID(),
                createdAt: passkey.hasCreatedAt ? passkey.createdAt.date : nil,
                lastUsedAt: passkey.hasLastUsedAt ? passkey.lastUsedAt.date : nil
            )
        }
    }

    func beginLogin() async throws -> PasskeyOptionsPayload {
        let response = await client.beginPasskeyLogin(request: Relay_Communication_V1_BeginPasskeyLoginRequest())
        let result = try response.result.get()
        return PasskeyOptionsPayload(ceremonyID: result.ceremonyID, optionsJSON: result.optionsJson)
    }

    func finishLogin(ceremonyID: String, credentialJSON: Data) async throws -> ServerSession {
        let response = await client.finishPasskeyLogin(
            request: Relay_Communication_V1_FinishPasskeyLoginRequest.with {
                $0.ceremonyID = ceremonyID
                $0.credentialJson = credentialJSON
            }
        )
        return Self.session(from: try response.result.get())
    }

    private static func session(from response: Relay_Communication_V1_FinishPasskeyLoginResponse) -> ServerSession {
        ServerSession(
            accessToken: response.accessToken,
            refreshToken: response.refreshToken,
            accessExpiresAt: response.accessExpiresAt.date,
            refreshExpiresAt: response.refreshExpiresAt.date
        )
    }

    private static func session(from response: Relay_Communication_V1_BootstrapResponse) -> ServerSession {
        ServerSession(
            accessToken: response.accessToken,
            refreshToken: response.refreshToken,
            accessExpiresAt: response.accessExpiresAt.date,
            refreshExpiresAt: response.refreshExpiresAt.date
        )
    }

    private static func session(from response: Relay_Communication_V1_RefreshResponse) -> ServerSession {
        ServerSession(
            accessToken: response.accessToken,
            refreshToken: response.refreshToken,
            accessExpiresAt: response.accessExpiresAt.date,
            refreshExpiresAt: response.refreshExpiresAt.date
        )
    }
}

@MainActor
final class URLSessionCommunicationSyncClientFactory: CommunicationSyncClientCreating {
    func makeClient(baseURL: URL) -> any CommunicationSyncClient {
        ConnectCommunicationSyncClient(baseURL: baseURL)
    }
}

@MainActor
private final class ConnectCommunicationSyncClient: CommunicationSyncClient {
    private let client: Relay_Communication_V1_CommunicationSyncServiceClient

    init(baseURL: URL) {
        let config = ProtocolClientConfig(host: baseURL.absoluteString, codec: ProtoCodec())
        client = Relay_Communication_V1_CommunicationSyncServiceClient(
            client: ProtocolClient(httpClient: URLSessionHTTPClient(), config: config)
        )
    }

    func pullEvents(afterSequence: Int64, accessToken: String) async throws -> EncryptedEventPage {
        let response = await client.pullEvents(
            request: Relay_Communication_V1_PullEventsRequest.with {
                $0.afterSequence = afterSequence
                $0.pageSize = 100
            },
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        return try ServerConnectionManager.eventPage(from: response.result.get())
    }

    func watchEvents(afterSequence: Int64, accessToken: String) throws -> any EncryptedEventStream {
        let stream = client.watchEvents(headers: ["authorization": ["Bearer \(accessToken)"]])
        return try ServerConnectEncryptedEventStream(stream: stream, afterSequence: afterSequence)
    }

    func getHistorySnapshot(kind: Relay_Communication_V1_HistoryKind, accessToken: String) async throws -> HistorySnapshotPayload {
        let response = await client.getHistorySnapshot(
            request: Relay_Communication_V1_GetHistorySnapshotRequest.with { $0.kind = kind },
            headers: ["authorization": ["Bearer \(accessToken)"]]
        )
        let snapshot = try response.result.get().snapshot
        return HistorySnapshotPayload(revision: snapshot.revision, ciphertext: snapshot.ciphertext)
    }
}

@MainActor
@Observable
final class ServerConnectionManager {
    @ObservationIgnored private static let logger = Logger(
        subsystem: "com.nowcent.simdeck",
        category: "ServerConnection"
    )
    var address: String
    private(set) var state: ServerConnectionState
    private(set) var lastErrorDescription: String?

    @ObservationIgnored private let network: any ServerNetworking
    @ObservationIgnored private let sessionStore: any SecureValueStoring
    @ObservationIgnored private let communicationSyncClientFactory: any CommunicationSyncClientCreating
    @ObservationIgnored private let passkeyRPCClientFactory: any PasskeyRPCClientCreating
    @ObservationIgnored private let ownerRPCClientFactory: any OwnerRPCClientCreating
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private var session: ServerSession?
    @ObservationIgnored private var refreshTask: Task<ServerSession, Error>?
    @ObservationIgnored private var deviceIdentityKeys: [UUID: Data] = [:]

    private static let addressKey = "sim-deck.server-address.v1"

    init(
        network: any ServerNetworking,
        sessionStore: any SecureValueStoring,
        defaults: UserDefaults,
        communicationSyncClientFactory: any CommunicationSyncClientCreating = URLSessionCommunicationSyncClientFactory(),
        passkeyRPCClientFactory: any PasskeyRPCClientCreating = ConnectPasskeyRPCClientFactory(),
        ownerRPCClientFactory: (any OwnerRPCClientCreating)? = nil
    ) {
        self.network = network
        self.sessionStore = sessionStore
        self.defaults = defaults
        self.communicationSyncClientFactory = communicationSyncClientFactory
        self.passkeyRPCClientFactory = passkeyRPCClientFactory
        self.ownerRPCClientFactory = ownerRPCClientFactory ?? network.ownerRPCClientFactory() ?? ConnectOwnerRPCClientFactory()
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

    private func ownerService() async throws -> (Relay_Communication_V1_OwnerServiceClientInterface, String) {
        let baseURL = try validatedBaseURL()
        let session = try await validSession(baseURL: baseURL)
        return (ownerRPCClientFactory.makeClient(baseURL: baseURL).service, session.accessToken)
    }

    func markRequestFailed(_ error: Error) {
        state = .failed
        lastErrorDescription = error.localizedDescription
        Self.logger.error("Server operation failed: \(String(reflecting: error), privacy: .public)")
    }

    var authenticatedBaseURL: URL? { try? validatedBaseURL() }
    var configuredBaseURL: URL? { try? validatedBaseURL() }
    var accessToken: String? { session?.accessToken }

    func acceptWebPasskeySession(_ webSession: WebPasskeySession) throws {
        try save(ServerSession(
            accessToken: webSession.accessToken,
            refreshToken: webSession.refreshToken,
            accessExpiresAt: webSession.accessExpiresAt,
            refreshExpiresAt: webSession.refreshExpiresAt
        ))
        state = .connected
        lastErrorDescription = nil
    }

    func signIn(password: String) async throws {
        state = .connecting
        lastErrorDescription = nil
        do {
            let baseURL = try validatedBaseURL()
            let session = try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
                .bootstrap(username: "admin", password: password)
            try save(session)
            defaults.set(address.trimmingCharacters(in: .whitespacesAndNewlines), forKey: Self.addressKey)
            state = .connected
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            Self.logger.error("Server sign-in failed: \(String(reflecting: error), privacy: .public)")
            throw error
        }
    }

    func beginPasskeyRegistration() async throws -> PasskeyRegistrationCeremony {
        let baseURL = try validatedBaseURL()
        let session = try await validSession(baseURL: baseURL)
        let result = try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
            .beginRegistration(accessToken: session.accessToken)
        guard let options = try? Self.decoder.decode(PasskeyOptions.self, from: result.optionsJSON),
              let relyingPartyID = options.publicKey.relyingParty?.id,
              let user = options.publicKey.user,
              let challenge = Base64URL.decode(options.publicKey.challenge),
              let userID = Base64URL.decode(user.id),
              !result.ceremonyID.isEmpty,
              !relyingPartyID.isEmpty,
              !user.name.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        return PasskeyRegistrationCeremony(
            id: result.ceremonyID,
            challenge: PasskeyRegistrationChallenge(
                relyingPartyID: relyingPartyID,
                challenge: challenge,
                userID: userID,
                userName: user.name
            )
        )
    }

    func listPasskeys() async throws -> [PasskeySummary] {
        let baseURL = try validatedBaseURL()
        let session = try await validSession(baseURL: baseURL)
        return try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
            .listPasskeys(accessToken: session.accessToken)
    }

    func finishPasskeyRegistration(
        ceremonyID: String,
        credential: PasskeyRegistrationCredential
    ) async throws {
        guard !ceremonyID.isEmpty,
              !credential.credentialID.isEmpty,
              !credential.clientDataJSON.isEmpty,
              !credential.attestationObject.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        let credentialID = Base64URL.encode(credential.credentialID)
        let credentialJSON = try Self.encoder.encode(PasskeyRegistrationFinishRequest(
            id: credentialID,
            rawID: credentialID,
            type: "public-key",
            response: PasskeyRegistrationFinishResponse(
                clientDataJSON: Base64URL.encode(credential.clientDataJSON),
                attestationObject: Base64URL.encode(credential.attestationObject)
            )
        ))
        let baseURL = try validatedBaseURL()
        let session = try await validSession(baseURL: baseURL)
        try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
            .finishRegistration(ceremonyID: ceremonyID, credentialJSON: credentialJSON, accessToken: session.accessToken)
    }

    func beginPasskeyLogin() async throws -> PasskeyAssertionCeremony {
        state = .connecting
        lastErrorDescription = nil
        do {
            let baseURL = try validatedBaseURL()
            let result = try await passkeyRPCClientFactory.makeClient(baseURL: baseURL).beginLogin()
            guard let options = try? Self.decoder.decode(PasskeyOptions.self, from: result.optionsJSON),
                  let relyingPartyID = options.publicKey.relyingPartyID,
                  let challenge = Base64URL.decode(options.publicKey.challenge),
                  !result.ceremonyID.isEmpty,
                  !relyingPartyID.isEmpty else {
                throw ServerConnectionError.invalidResponse
            }
            return PasskeyAssertionCeremony(
                id: result.ceremonyID,
                challenge: PasskeyAssertionChallenge(
                    relyingPartyID: relyingPartyID,
                    challenge: challenge
                )
            )
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            Self.logger.error("Passkey login start failed: \(String(reflecting: error), privacy: .public)")
            throw error
        }
    }

    func finishPasskeyLogin(
        ceremonyID: String,
        credential: PasskeyAssertionCredential
    ) async throws {
        guard !ceremonyID.isEmpty,
              !credential.credentialID.isEmpty,
              !credential.clientDataJSON.isEmpty,
              !credential.authenticatorData.isEmpty,
              !credential.signature.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        do {
            let credentialID = Base64URL.encode(credential.credentialID)
            let credentialJSON = try Self.encoder.encode(PasskeyAssertionFinishRequest(
                id: credentialID,
                rawID: credentialID,
                type: "public-key",
                response: PasskeyAssertionFinishResponse(
                    clientDataJSON: Base64URL.encode(credential.clientDataJSON),
                    authenticatorData: Base64URL.encode(credential.authenticatorData),
                    signature: Base64URL.encode(credential.signature),
                    userHandle: credential.userID.map(Base64URL.encode)
                )
            ))
            let baseURL = try validatedBaseURL()
            let session = try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
                .finishLogin(ceremonyID: ceremonyID, credentialJSON: credentialJSON)
            try save(session)
            defaults.set(address.trimmingCharacters(in: .whitespacesAndNewlines), forKey: Self.addressKey)
            state = .connected
            lastErrorDescription = nil
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            Self.logger.error("Passkey login finish failed: \(String(reflecting: error), privacy: .public)")
            throw error
        }
    }

    func signOut() throws {
        try sessionStore.delete()
        session = nil
        state = .disconnected
        lastErrorDescription = nil
    }

    func revokeCurrentSession() async throws {
        let baseURL = try validatedBaseURL()
        let session = try await validSession(baseURL: baseURL)
        try await passkeyRPCClientFactory.makeClient(baseURL: baseURL).logout(accessToken: session.accessToken)
        try signOut()
    }

    func loadDevices(profileKey: VaultKey? = nil, now: Date = .now) async throws -> [DeviceLineIdentity] {
        do {
            let (service, token) = try await ownerService()
            let body = try await service.listDevices(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
            deviceIdentityKeys = Dictionary(
                uniqueKeysWithValues: body.devices.compactMap { device in
                    guard device.identityPublicKey.count == 32, let uuid = UUID(uuidString: device.deviceUuid) else { return nil }
                    return (uuid, device.identityPublicKey)
                }
            )
            NotificationSharedState.cacheDeviceKeys(
                body.devices.reduce(into: [UUID: CachedDeviceKeys]()) { result, device in
                    guard device.identityPublicKey.count == 32,
                          device.commandPublicKey.count == 32,
                          let uuid = UUID(uuidString: device.deviceUuid) else { return }
                    result[uuid] = CachedDeviceKeys(
                        verificationKey: device.identityPublicKey,
                        commandKey: device.commandPublicKey
                    )
                }
            )
            let devices = try body.devices.flatMap { device in
                guard let deviceUUID = UUID(uuidString: device.deviceUuid) else { throw ServerConnectionError.invalidResponse }
                let lastSeenAt = device.hasLastSeenAt ? device.lastSeenAt.date : nil
                let recentlySeen = Self.isFresh(lastSeenAt, now: now)
                let isOnline = recentlySeen && device.commandPublicKey.count == 32
                let radioUpdatedAt = device.hasRadioStatusUpdatedAt ? device.radioStatusUpdatedAt.date : nil
                let hasFreshRadioStatus = Self.isFresh(radioUpdatedAt, now: now)
                let radioStatus: CellularRadioStatus? = if isOnline && hasFreshRadioStatus {
                    CellularRadioStatus(
                        technology: CellularRadioAccessTechnology(rawValue: device.radioAccessTechnology) ?? .unknown,
                        signalBars: device.hasSignalBars ? Int(device.signalBars) : nil
                    )
                } else {
                    nil
                }
                let profileCiphertext = device.profileCiphertext
                let profile: DeviceProfile
                if profileCiphertext.isEmpty {
                    profile = .empty
                } else if let profileKey {
                    profile = try DeviceProfileCodec.open(profileCiphertext, using: profileKey)
                } else {
                    profile = .empty
                }
                let slots = device.slots.isEmpty ? [ServerDeviceSlot(slotID: nil, simID: nil, numericID: Int(device.numericID), profileCiphertext: nil, radioAccessTechnology: CellularRadioAccessTechnology(rawValue: device.radioAccessTechnology), signalBars: device.hasSignalBars ? Int(device.signalBars) : nil, radioStatusUpdatedAt: radioUpdatedAt)] : device.slots.map { ServerDeviceSlot(slotID: UUID(uuidString: $0.slotID), simID: $0.hasSimID ? UUID(uuidString: $0.simID) : nil, numericID: $0.hasSimNumericID ? Int($0.simNumericID) : nil, profileCiphertext: $0.profileCiphertext, radioAccessTechnology: CellularRadioAccessTechnology(rawValue: $0.radioAccessTechnology), signalBars: $0.hasSignalBars ? Int($0.signalBars) : nil, radioStatusUpdatedAt: $0.hasRadioStatusUpdatedAt ? $0.radioStatusUpdatedAt.date : nil, slotKey: $0.slotKey, slotIndex: Int($0.slotIndex)) }
                return try slots.map { slot in
                    let slotProfile: DeviceProfile
                    if let ciphertext = slot.profileCiphertext, !ciphertext.isEmpty, let profileKey {
                        slotProfile = try DeviceProfileCodec.open(ciphertext, using: profileKey)
                    } else {
                        slotProfile = profile
                    }
                    let slotFresh = Self.isFresh(slot.radioStatusUpdatedAt, now: now)
                    let slotRadioStatus: CellularRadioStatus? = if isOnline && slotFresh {
                        CellularRadioStatus(technology: slot.radioAccessTechnology ?? .unknown, signalBars: slot.signalBars)
                    } else { radioStatus }
                    return DeviceLineIdentity(
                        // Commands still target the modem device; the line identity targets its slot.
                        id: slot.slotID ?? slot.simID ?? deviceUUID,
                        deviceID: deviceUUID,
                        slotID: slot.slotID,
                        simID: slot.simID,
                        numericID: slot.numericID ?? Int(device.numericID),
                        slotKey: slot.slotKey,
                        slotIndex: slot.slotIndex,
                        deviceName: profile.deviceName,
                        lineName: slotProfile.lineName,
                        phoneNumber: slotProfile.phoneNumber,
                        isOnline: isOnline,
                        radioStatus: slotRadioStatus,
                        commandPublicKey: device.commandPublicKey,
                        simKind: slotProfile.simKind
                    )
                }
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

    func loadSIMCards(profileKey: VaultKey? = nil) async throws -> [ManagedSIM] {
        let (service, token) = try await ownerService()
        let body = try await service.listSimCards(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
        return try body.sims.map { sim in
            guard let id = UUID(uuidString: sim.simID) else { throw ServerConnectionError.invalidResponse }
            let profile = if let profileKey, !sim.profileCiphertext.isEmpty {
                try DeviceProfileCodec.open(sim.profileCiphertext, using: profileKey)
            } else {
                DeviceProfile.empty
            }
            return ManagedSIM(
                id: id,
                numericID: Int(sim.numericID),
                assignedDeviceID: sim.hasAssignedDeviceUuid ? UUID(uuidString: sim.assignedDeviceUuid) : nil,
                assignedSlotID: sim.hasAssignedSlotID ? UUID(uuidString: sim.assignedSlotID) : nil,
                lastSeenAt: sim.lastSeenAt.date,
                lineName: profile.lineName,
                phoneNumber: profile.phoneNumber
            )
        }
    }

    func retireSIMCard(simID: UUID) async throws {
        let (service, token) = try await ownerService()
        _ = try await service.retireSimCard(
            request: .with { $0.simID = simID.uuidString.lowercased() },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState {
        let (service, token) = try await ownerService()
        let response = try await service.openDeviceDiscovery(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
        return Self.discoverySessionState(active: response.active, session: response.hasSession ? response.session : nil)
    }

    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState {
        let (service, token) = try await ownerService()
        let response = try await service.getDeviceDiscoverySession(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
        return Self.discoverySessionState(active: response.active, session: response.hasSession ? response.session : nil)
    }

    func closeDeviceDiscovery() async throws {
        let (service, token) = try await ownerService()
        _ = try await service.closeDeviceDiscovery(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
    }

    func discoveredDevices() async throws -> [DiscoveredDevice] {
        let (service, token) = try await ownerService()
        let response = try await service.listDeviceDiscoveries(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
        return try response.discoveries.map { item in
            guard let id = UUID(uuidString: item.deviceUuid), let first = item.hasFirstSeenAt ? Optional(item.firstSeenAt.date) : nil, let last = item.hasLastSeenAt ? Optional(item.lastSeenAt.date) : nil, let expires = item.hasExpiresAt ? Optional(item.expiresAt.date) : nil, let status = DeviceDiscoveryStatus(rawValue: item.status) else { throw ServerConnectionError.invalidResponse }
            return DiscoveredDevice(deviceUUID: id, identityPublicKey: item.identityPublicKey, commandPublicKey: item.commandPublicKey, status: status, firstSeenAt: first, lastSeenAt: last, expiresAt: expires)
        }
    }

    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice {
        let (service, token) = try await ownerService()
        let response = try await service.approveDeviceDiscovery(request: .with { $0.deviceUuid = id.uuidString.lowercased() }, headers: ["authorization": ["Bearer \(token)"]]).result.get()
        guard response.hasDevice, let deviceID = UUID(uuidString: response.device.deviceUuid) else { throw ServerConnectionError.invalidResponse }
        return ApprovedDevice(deviceUUID: deviceID, numericID: Int(response.device.numericID))
    }

    func rejectDiscoveredDevice(id: UUID) async throws {
        let (service, token) = try await ownerService()
        _ = try await service.rejectDeviceDiscovery(request: .with { $0.deviceUuid = id.uuidString.lowercased() }, headers: ["authorization": ["Bearer \(token)"]]).result.get()
    }

    private static func discoverySessionState(active: Bool, session: Relay_Communication_V1_DeviceDiscoverySession?) -> DeviceDiscoverySessionState {
        guard let session else { return DeviceDiscoverySessionState(active: active, session: nil) }
        return DeviceDiscoverySessionState(active: active, session: DeviceDiscoverySession(openedAt: session.openedAt.date, expiresAt: session.expiresAt.date, closedAt: session.hasClosedAt ? session.closedAt.date : nil))
    }

    private static func appInstallationTrust(
        from installation: Relay_Communication_V1_AppInstallation
    ) throws -> AppInstallationTrust {
        guard let installationID = UUID(uuidString: installation.installationID), installation.hasCreatedAt else {
            throw ServerConnectionError.invalidResponse
        }
        return AppInstallationTrust(
            id: installationID,
            identityPublicKey: installation.identityPublicKey,
            keyAgreementPublicKey: installation.keyAgreementPublicKey,
            wrappedVaultKey: installation.wrappedVaultKey.isEmpty ? nil : installation.wrappedVaultKey,
            approvedAt: installation.hasApprovedAt ? installation.approvedAt.date : nil,
            revokedAt: installation.hasRevokedAt ? installation.revokedAt.date : nil,
            createdAt: installation.createdAt.date
        )
    }

    func updateDeviceNumericID(deviceID: UUID, numericID: Int) async throws {
        guard (1...99).contains(numericID) else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        _ = try await service.updateDeviceNumericID(
            request: .with {
                $0.id = deviceID.uuidString.lowercased()
                $0.numericID = Int32(numericID)
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func updateDeviceProfile(deviceID: UUID, profileCiphertext: Data) async throws {
        guard !profileCiphertext.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        _ = try await service.updateDeviceProfile(
            request: .with {
                $0.id = deviceID.uuidString.lowercased()
                $0.profileCiphertext = profileCiphertext
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func updateSIMNumericID(simID: UUID, numericID: Int) async throws {
        guard (1...99).contains(numericID) else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        _ = try await service.updateSimNumericID(
            request: .with {
                $0.id = simID.uuidString.lowercased()
                $0.numericID = Int32(numericID)
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func updateSIMProfile(simID: UUID, profileCiphertext: Data) async throws {
        guard !profileCiphertext.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        _ = try await service.updateSimProfile(
            request: .with {
                $0.id = simID.uuidString.lowercased()
                $0.profileCiphertext = profileCiphertext
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func listESIMProfiles(simID: UUID) async throws -> [Relay_Communication_V1_EsimProfile] {
        let (service, token) = try await ownerService()
        let response = try await service.listEsimProfiles(
            request: .with { $0.simID = simID.uuidString.lowercased() },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        return response.profiles
    }

    func addESIMProfile(
        simID: UUID,
        profileID: String,
        name: String = "",
        iccid: String = "",
        operatorName: String = ""
    ) async throws -> Relay_Communication_V1_EsimProfile {
        let (service, token) = try await ownerService()
        let response = try await service.addEsimProfile(
            request: .with {
                $0.simID = simID.uuidString.lowercased()
                $0.profileID = profileID
                $0.name = name
                $0.iccid = iccid
                $0.operatorName = operatorName
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard response.hasProfile else { throw ServerConnectionError.invalidResponse }
        return response.profile
    }

    func renameESIMProfile(simID: UUID, profileID: String, name: String) async throws -> Relay_Communication_V1_EsimProfile {
        let (service, token) = try await ownerService()
        let response = try await service.renameEsimProfile(
            request: .with {
                $0.simID = simID.uuidString.lowercased()
                $0.profileID = profileID
                $0.name = name
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard response.hasProfile else { throw ServerConnectionError.invalidResponse }
        return response.profile
    }

    func setESIMProfileEnabled(simID: UUID, profileID: String, enabled: Bool) async throws -> Relay_Communication_V1_EsimProfile {
        let (service, token) = try await ownerService()
        let response = try await service.setEsimProfileEnabled(
            request: .with {
                $0.simID = simID.uuidString.lowercased()
                $0.profileID = profileID
                $0.enabled = enabled
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard response.hasProfile else { throw ServerConnectionError.invalidResponse }
        return response.profile
    }

    func deleteESIMProfile(simID: UUID, profileID: String) async throws {
        let (service, token) = try await ownerService()
        _ = try await service.deleteEsimProfile(
            request: .with {
                $0.simID = simID.uuidString.lowercased()
                $0.profileID = profileID
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func revokeDevice(deviceID: UUID) async throws {
        let (service, token) = try await ownerService()
        _ = try await service.revokeDevice(
            request: .with { $0.deviceUuid = deviceID.uuidString.lowercased() },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    private static func isFresh(_ date: Date?, now: Date) -> Bool {
        guard let date else { return false }
        let age = now.timeIntervalSince(date)
        return age >= 0 && age < 60
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        let (service, token) = try await ownerService()
        let response = try await service.getCommandRecipient(request: .with { $0.deviceUuid = deviceID.uuidString.lowercased() }, headers: ["authorization": ["Bearer \(token)"]]).result.get()
        guard response.commandPublicKey.count == 32 else { throw ServerConnectionError.deviceUnavailable }
        return response.commandPublicKey
    }

    func commandTarget(for line: DeviceLineIdentity) async throws -> EncryptedCommandTarget {
        let candidates = try await loadDevices().filter { $0.deviceID == line.deviceID }
        let currentLine: DeviceLineIdentity?
        if let slotKey = line.slotKey, !slotKey.isEmpty {
            currentLine = candidates.first {
                $0.slotKey == slotKey && (line.slotIndex == nil || $0.slotIndex == line.slotIndex)
            }
        } else if let slotID = line.slotID {
            currentLine = candidates.first { $0.slotID == slotID }
        } else if let simID = line.simID {
            currentLine = candidates.first { $0.simID == simID }
        } else {
            currentLine = candidates.count == 1 ? candidates[0] : nil
        }
        guard let currentLine,
              currentLine.isOnline,
              let publicKey = currentLine.commandPublicKey,
              publicKey.count == 32 else {
            throw ServerConnectionError.deviceUnavailable
        }
        return EncryptedCommandTarget(
            deviceID: currentLine.deviceID,
            slotID: currentLine.slotID,
            simID: currentLine.simID,
            publicKey: publicKey
        )
    }

    func turnCredentials() async throws -> CallTURNCredentials {
        let (service, token) = try await ownerService()
        let response = try await service.getTurnCredentials(request: .init(), headers: ["authorization": ["Bearer \(token)"]]).result.get()
        let credentials = CallTURNCredentials(urls: response.urls, username: response.username, credential: response.credential, expiresAt: response.expiresAt.date)
        guard !credentials.urls.isEmpty,
              credentials.urls.allSatisfy({ url in
                  guard let scheme = URLComponents(string: url)?.scheme else { return false }
                  return ["turn", "turns"].contains(scheme)
              }),
              !credentials.username.isEmpty,
              !credentials.credential.isEmpty,
              credentials.expiresAt > Date() else {
            throw ServerConnectionError.invalidResponse
        }
        return credentials
    }

    func eventVerificationKey(deviceID: UUID) async throws -> Data {
        if let publicKey = deviceIdentityKeys[deviceID], publicKey.count == 32 {
            return publicKey
        }
        _ = try await loadDevices()
        guard let publicKey = deviceIdentityKeys[deviceID], publicKey.count == 32 else {
            throw ServerConnectionError.deviceUnavailable
        }
        return publicKey
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        guard command.senderPublicKey.count == 32, command.nonce.count == 12,
              !command.ciphertext.isEmpty, command.ciphertext.count <= 64 * 1024,
              command.signature.count == 64,
              command.expiresAt > Date(),
              command.expiresAt <= command.createdAt.addingTimeInterval(5 * 60) else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        let response = try await service.enqueueCommand(
            request: .with {
                $0.command = .with {
                    $0.id = command.id.uuidString.lowercased()
                    $0.deviceUuid = deviceID.uuidString.lowercased()
                    $0.senderInstallationID = command.senderInstallationID.uuidString.lowercased()
                    $0.senderPublicKey = command.senderPublicKey
                    $0.nonce = command.nonce
                    $0.ciphertext = command.ciphertext
                    $0.signature = command.signature
                    $0.createdAt = Google_Protobuf_Timestamp(date: command.createdAt)
                    $0.expiresAt = Google_Protobuf_Timestamp(date: command.expiresAt)
                }
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard UUID(uuidString: response.commandID) == command.id else { throw ServerConnectionError.invalidResponse }
    }

    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus {
        let (service, token) = try await ownerService()
        let response = try await service.getCommandStatus(
            request: .with {
                $0.deviceUuid = deviceID.uuidString.lowercased()
                $0.commandID = commandID.uuidString.lowercased()
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard let id = UUID(uuidString: response.commandID), let returnedDevice = UUID(uuidString: response.deviceUuid), returnedDevice == deviceID else { throw ServerConnectionError.invalidResponse }
        let result: EncryptedCallCommandResult? = if response.hasResultNonce && response.hasResultCiphertext {
            EncryptedCallCommandResult(nonce: response.resultNonce, ciphertext: response.resultCiphertext)
        } else { nil }
        return EncryptedCallCommandStatus(id: id, deviceUUID: returnedDevice, state: response.state, result: result)
    }

    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage {
        guard afterSequence >= 0 else { throw ServerConnectionError.invalidResponse }
        let baseURL = try validatedBaseURL()
        let activeSession = try await validSession(baseURL: baseURL)
        return try await communicationSyncClientFactory.makeClient(baseURL: baseURL)
            .pullEvents(afterSequence: afterSequence, accessToken: activeSession.accessToken)
    }

    func getHistorySnapshot(kind: Relay_Communication_V1_HistoryKind) async throws -> HistorySnapshotPayload {
        let baseURL = try validatedBaseURL()
        let activeSession = try await validSession(baseURL: baseURL)
        return try await communicationSyncClientFactory.makeClient(baseURL: baseURL)
            .getHistorySnapshot(kind: kind, accessToken: activeSession.accessToken)
    }

    func openEncryptedEventStream(
        afterSequence: Int64
    ) async throws -> any EncryptedEventStream {
        guard afterSequence >= 0 else {
            throw ServerConnectionError.invalidResponse
        }
        let baseURL = try validatedBaseURL()
        let activeSession = try await validSession(baseURL: baseURL)
        return try communicationSyncClientFactory.makeClient(baseURL: baseURL)
            .watchEvents(afterSequence: afterSequence, accessToken: activeSession.accessToken)
    }

    func registerAppInstallation(
        _ registration: AppInstallationRegistration
    ) async throws -> AppInstallationTrust {
        guard registration.identityPublicKey.count == 32,
              registration.keyAgreementPublicKey.count == 32 else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, token) = try await ownerService()
        let response = try await service.registerAppInstallation(
            request: .with {
                $0.installationID = registration.id.uuidString.lowercased()
                $0.identityPublicKey = registration.identityPublicKey
                $0.keyAgreementPublicKey = registration.keyAgreementPublicKey
                $0.wrappedVaultKey = registration.wrappedVaultKey ?? Data()
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard response.hasInstallation else { throw ServerConnectionError.invalidResponse }
        return try Self.appInstallationTrust(from: response.installation)
    }

    func claimIncomingCall(
        notificationID: UUID,
        installationID: UUID
    ) async throws -> IncomingCallClaim {
        let (service, token) = try await ownerService()
        let response = try await service.claimIncomingCall(
            request: .with {
                $0.notificationID = notificationID.uuidString.lowercased()
                $0.installationID = installationID.uuidString.lowercased()
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
        guard response.hasClaimedAt,
              response.notificationID == notificationID.uuidString.lowercased(),
              response.installationID == installationID.uuidString.lowercased() else {
            throw ServerConnectionError.invalidResponse
        }
        return IncomingCallClaim(notificationID: notificationID, installationID: installationID, claimedAt: response.claimedAt.date, won: response.won)
    }

    func releaseIncomingCall(
        notificationID: UUID,
        installationID: UUID
    ) async throws {
        let (service, token) = try await ownerService()
        _ = try await service.releaseIncomingCall(
            request: .with {
                $0.notificationID = notificationID.uuidString.lowercased()
                $0.installationID = installationID.uuidString.lowercased()
            },
            headers: ["authorization": ["Bearer \(token)"]]
        ).result.get()
    }

    func registerPushToken(
        _ token: Data,
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws {
        guard !token.isEmpty, token.count <= 256 else {
            throw ServerConnectionError.invalidResponse
        }
        let (service, accessToken) = try await ownerService()
        _ = try await service.upsertPushToken(
            request: .with {
                $0.installationID = installationID.uuidString.lowercased()
                $0.kind = kind.rawValue
                $0.environment = environment.rawValue
                $0.token = token
            },
            headers: ["authorization": ["Bearer \(accessToken)"]]
        ).result.get()
    }

    func unregisterPushToken(
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws {
        let (service, accessToken) = try await ownerService()
        do {
            _ = try await service.deletePushToken(
                request: .with {
                    $0.installationID = installationID.uuidString.lowercased()
                    $0.kind = kind.rawValue
                    $0.environment = environment.rawValue
                },
                headers: ["authorization": ["Bearer \(accessToken)"]]
            ).result.get()
        } catch {
            if error.code == .notFound {
                return
            }
            throw error
        }
    }

    private func validSession(baseURL: URL) async throws -> ServerSession {
        guard let session else { throw ServerConnectionError.authenticationRequired }
        if session.accessExpiresAt > Date().addingTimeInterval(15) {
            return session
        }
        return try await refresh(baseURL: baseURL, session: session)
    }

    private func refresh(baseURL: URL, session: ServerSession) async throws -> ServerSession {
        if let currentSession = self.session,
           currentSession.refreshToken != session.refreshToken,
           currentSession.accessExpiresAt > Date().addingTimeInterval(15) {
            return currentSession
        }
        if let refreshTask {
            return try await refreshTask.value
        }
        let task = Task { @MainActor in
            try await performRefresh(baseURL: baseURL, session: session)
        }
        refreshTask = task
        defer { refreshTask = nil }
        return try await task.value
    }

    private func performRefresh(baseURL: URL, session: ServerSession) async throws -> ServerSession {
        guard session.refreshExpiresAt > .now else {
            throw ServerConnectionError.authenticationRequired
        }
        let replacement = try await passkeyRPCClientFactory.makeClient(baseURL: baseURL)
            .refresh(refreshToken: session.refreshToken)
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

    fileprivate static func eventPage(
        from response: Relay_Communication_V1_PullEventsResponse
    ) throws -> EncryptedEventPage {
        let events = try response.events.map(eventEnvelope(from:))
        if let last = events.last, response.nextSequence != last.sequence {
            throw ServerConnectionError.invalidResponse
        }
        return EncryptedEventPage(events: events, nextSequence: response.nextSequence)
    }

    static func eventEnvelope(
        from event: Relay_Communication_V1_EncryptedEvent
    ) throws -> EncryptedEventEnvelope {
        guard let id = UUID(uuidString: event.id),
              let deviceID = UUID(uuidString: event.deviceUuid),
              event.hasOccurredAt else {
            throw ServerConnectionError.invalidResponse
        }
        let recipientID = event.hasRecipientInstallationID
            ? UUID(uuidString: event.recipientInstallationID)
            : nil
        if event.hasRecipientInstallationID && recipientID == nil {
            throw ServerConnectionError.invalidResponse
        }
        return EncryptedEventEnvelope(
            id: id,
            sequence: event.sequence,
            type: event.type,
            deviceUUID: deviceID,
            recipientInstallationID: recipientID,
            occurredAt: event.occurredAt.date,
            keyEpoch: Int(event.keyEpoch),
            nonce: event.nonce,
            ciphertext: event.ciphertext,
            signature: event.signature
        )
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

extension ServerConnectionManager: EncryptedCallCommandAPI, CallTURNCredentialProviding {}
extension ServerConnectionManager: EncryptedEventStreamOpening {}
extension ServerConnectionManager: IncomingCallClaiming, IncomingCallClaimReleasing {}
extension ServerConnectionManager: PushTokenRegistering {}
extension ServerConnectionManager: DeviceDiscoveryAPI {}

private struct PasskeyOptions: Decodable {
    let publicKey: PasskeyPublicKeyOptions
}

private struct PasskeyPublicKeyOptions: Decodable {
    let challenge: String
    let relyingParty: PasskeyRelyingParty?
    let relyingPartyID: String?
    let user: PasskeyUser?

    enum CodingKeys: String, CodingKey {
        case challenge
        case relyingParty = "rp"
        case relyingPartyID = "rpId"
        case user
    }
}

private struct PasskeyRelyingParty: Decodable {
    let id: String?
}

private struct PasskeyUser: Decodable {
    let id: String
    let name: String
}

private struct PasskeyRegistrationFinishRequest: Encodable {
    let id: String
    let rawID: String
    let type: String
    let response: PasskeyRegistrationFinishResponse

    enum CodingKeys: String, CodingKey {
        case id
        case rawID = "rawId"
        case type
        case response
    }
}

private struct PasskeyRegistrationFinishResponse: Encodable {
    let clientDataJSON: String
    let attestationObject: String
}

private struct PasskeyAssertionFinishRequest: Encodable {
    let id: String
    let rawID: String
    let type: String
    let response: PasskeyAssertionFinishResponse

    enum CodingKeys: String, CodingKey {
        case id
        case rawID = "rawId"
        case type
        case response
    }
}

private struct PasskeyAssertionFinishResponse: Encodable {
    let clientDataJSON: String
    let authenticatorData: String
    let signature: String
    let userHandle: String?
}

private struct ServerDeviceSlot: Decodable {
    let slotID: UUID?
    let simID: UUID?
    let numericID: Int?
    let profileCiphertext: Data?
    let radioAccessTechnology: CellularRadioAccessTechnology?
    let signalBars: Int?
    let radioStatusUpdatedAt: Date?
    let slotKey: String
    let slotIndex: Int

    enum CodingKeys: String, CodingKey {
        case slotID = "slot_id"
        case simID = "sim_id"
        case numericID = "numeric_id"
        case profileCiphertext = "profile_ciphertext"
        case radioAccessTechnology = "radio_access_technology"
        case signalBars = "signal_bars"
        case radioStatusUpdatedAt = "radio_status_updated_at"
        case slotKey = "slot_key"
        case slotIndex = "slot_index"
    }

    init(slotID: UUID?, simID: UUID?, numericID: Int?, profileCiphertext: Data?, radioAccessTechnology: CellularRadioAccessTechnology?, signalBars: Int?, radioStatusUpdatedAt: Date?, slotKey: String = "1", slotIndex: Int = 1) {
        self.slotID = slotID
        self.simID = simID
        self.numericID = numericID
        self.profileCiphertext = profileCiphertext
        self.radioAccessTechnology = radioAccessTechnology
        self.signalBars = signalBars
        self.radioStatusUpdatedAt = radioStatusUpdatedAt
        self.slotKey = slotKey
        self.slotIndex = slotIndex
    }
}

private struct CommandStatusResponse: Decodable {
    let command: EncryptedCallCommandStatus
}
