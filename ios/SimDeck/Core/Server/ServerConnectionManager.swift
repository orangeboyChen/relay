import Foundation
import Observation
import OSLog
import Security

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

    init(from decoder: Decoder) throws {
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
    @ObservationIgnored private static let logger = Logger(
        subsystem: "com.nowcent.simdeck",
        category: "ServerConnection"
    )
    var address: String
    private(set) var state: ServerConnectionState
    private(set) var lastErrorDescription: String?

    @ObservationIgnored private let network: any ServerNetworking
    @ObservationIgnored private let sessionStore: any SecureValueStoring
    @ObservationIgnored private let webSocketFactory: any ServerWebSocketCreating
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private var session: ServerSession?
    @ObservationIgnored private var refreshTask: Task<ServerSession, Error>?
    @ObservationIgnored private var deviceIdentityKeys: [UUID: Data] = [:]

    private static let addressKey = "sim-deck.server-address.v1"

    init(
        network: any ServerNetworking,
        sessionStore: any SecureValueStoring,
        defaults: UserDefaults,
        webSocketFactory: any ServerWebSocketCreating = URLSessionServerWebSocketFactory()
    ) {
        self.network = network
        self.sessionStore = sessionStore
        self.defaults = defaults
        self.webSocketFactory = webSocketFactory
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

    func markRequestFailed(_ error: Error) {
        state = .failed
        lastErrorDescription = error.localizedDescription
        Self.logger.error("Server operation failed: \(String(reflecting: error), privacy: .public)")
    }

    var authenticatedBaseURL: URL? { try? validatedBaseURL() }
    var accessToken: String? { session?.accessToken }

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
            Self.logger.error("Server sign-in failed: \(String(reflecting: error), privacy: .public)")
            throw error
        }
    }

    func beginPasskeyRegistration() async throws -> PasskeyRegistrationCeremony {
        let (data, response) = try await authenticatedRequest(
            path: "v1/auth/passkey/registration/options",
            method: "POST"
        )
        guard response.statusCode == 200,
              let options = try? Self.decoder.decode(PasskeyOptionsResponse.self, from: data),
              let relyingPartyID = options.options.publicKey.relyingParty?.id,
              let user = options.options.publicKey.user,
              let challenge = Base64URL.decode(options.options.publicKey.challenge),
              let userID = Base64URL.decode(user.id),
              !options.ceremonyID.isEmpty,
              !relyingPartyID.isEmpty,
              !user.name.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        return PasskeyRegistrationCeremony(
            id: options.ceremonyID,
            challenge: PasskeyRegistrationChallenge(
                relyingPartyID: relyingPartyID,
                challenge: challenge,
                userID: userID,
                userName: user.name
            )
        )
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
        let body = try Self.encoder.encode(PasskeyRegistrationFinishRequest(
            id: credentialID,
            rawID: credentialID,
            type: "public-key",
            response: PasskeyRegistrationFinishResponse(
                clientDataJSON: Base64URL.encode(credential.clientDataJSON),
                attestationObject: Base64URL.encode(credential.attestationObject)
            )
        ))
        let (_, response) = try await authenticatedRequest(
            path: "v1/auth/passkey/registration/finish",
            method: "POST",
            body: body,
            queryItems: [URLQueryItem(name: "ceremony_id", value: ceremonyID)]
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func beginPasskeyLogin() async throws -> PasskeyAssertionCeremony {
        state = .connecting
        lastErrorDescription = nil
        do {
            let baseURL = try validatedBaseURL()
            var request = URLRequest(url: baseURL.appending(path: "v1/auth/passkey/login/options"))
            request.httpMethod = "POST"
            let (data, response) = try await network.data(for: request)
            guard response.statusCode == 200,
                  let options = try? Self.decoder.decode(PasskeyOptionsResponse.self, from: data),
                  let relyingPartyID = options.options.publicKey.relyingPartyID,
                  let challenge = Base64URL.decode(options.options.publicKey.challenge),
                  !options.ceremonyID.isEmpty,
                  !relyingPartyID.isEmpty else {
                throw ServerConnectionError.invalidResponse
            }
            return PasskeyAssertionCeremony(
                id: options.ceremonyID,
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
            let baseURL = try validatedBaseURL()
            let credentialID = Base64URL.encode(credential.credentialID)
            let body = try Self.encoder.encode(PasskeyAssertionFinishRequest(
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
            var components = URLComponents(
                url: baseURL.appending(path: "v1/auth/passkey/login/finish"),
                resolvingAgainstBaseURL: false
            )
            components?.queryItems = [URLQueryItem(name: "ceremony_id", value: ceremonyID)]
            guard let url = components?.url else {
                throw ServerConnectionError.invalidAddress
            }
            var request = URLRequest(url: url)
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = body
            let (data, response) = try await network.data(for: request)
            guard response.statusCode == 201 else {
                throw ServerConnectionError.requestFailed(response.statusCode)
            }
            let session = try Self.decoder.decode(ServerSession.self, from: data)
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
        let (_, response) = try await authenticatedRequest(path: "v1/auth/logout", method: "POST")
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        try signOut()
    }

    func loadDevices(profileKey: VaultKey? = nil, now: Date = .now) async throws -> [DeviceLineIdentity] {
        do {
            let (data, response) = try await authenticatedRequest(path: "v1/devices", method: "GET")
            guard response.statusCode == 200 else {
                throw ServerConnectionError.requestFailed(response.statusCode)
            }
            let body = try Self.decoder.decode(DeviceListResponse.self, from: data)
            deviceIdentityKeys = Dictionary(
                uniqueKeysWithValues: body.devices.compactMap { device in
                    guard device.identityPublicKey.count == 32 else { return nil }
                    return (device.deviceUUID, device.identityPublicKey)
                }
            )
            NotificationSharedState.cacheDeviceKeys(
                body.devices.reduce(into: [UUID: CachedDeviceKeys]()) { result, device in
                    guard device.identityPublicKey.count == 32,
                          let commandKey = device.commandPublicKey,
                          commandKey.count == 32 else { return }
                    result[device.deviceUUID] = CachedDeviceKeys(
                        verificationKey: device.identityPublicKey,
                        commandKey: commandKey
                    )
                }
            )
            let devices = try body.devices.flatMap { device in
                let recentlySeen = Self.isFresh(device.lastSeenAt, now: now)
                let isOnline = recentlySeen && device.commandPublicKey?.count == 32
                let hasFreshRadioStatus = Self.isFresh(device.radioStatusUpdatedAt, now: now)
                let radioStatus: CellularRadioStatus? = if isOnline && hasFreshRadioStatus {
                    CellularRadioStatus(
                        technology: device.radioAccessTechnology ?? .unknown,
                        signalBars: device.signalBars
                    )
                } else {
                    nil
                }
                let profileCiphertext = device.profileCiphertext ?? Data()
                let profile: DeviceProfile
                if profileCiphertext.isEmpty {
                    profile = .empty
                } else if let profileKey {
                    profile = try DeviceProfileCodec.open(profileCiphertext, using: profileKey)
                } else {
                    profile = .empty
                }
                let slots = device.slots.isEmpty ? [ServerDeviceSlot(slotID: nil, simID: nil, numericID: device.numericID, profileCiphertext: nil, radioAccessTechnology: device.radioAccessTechnology, signalBars: device.signalBars, radioStatusUpdatedAt: device.radioStatusUpdatedAt)] : device.slots
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
                        id: slot.slotID ?? slot.simID ?? device.deviceUUID,
                        deviceID: device.deviceUUID,
                        slotID: slot.slotID,
                        simID: slot.simID,
                        numericID: slot.numericID ?? device.numericID,
                        slotKey: slot.slotKey,
                        slotIndex: slot.slotIndex,
                        deviceName: profile.deviceName,
                        lineName: slotProfile.lineName,
                        phoneNumber: slotProfile.phoneNumber,
                        isOnline: isOnline,
                        radioStatus: slotRadioStatus,
                        commandPublicKey: device.commandPublicKey
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
        let (data, response) = try await authenticatedRequest(path: "v1/sims", method: "GET")
        guard response.statusCode == 200 else { throw ServerConnectionError.requestFailed(response.statusCode) }
        let body = try Self.decoder.decode(SIMListResponse.self, from: data)
        return try body.sims.map {
            let profile = if let profileKey, !$0.profileCiphertext.isEmpty {
                try DeviceProfileCodec.open($0.profileCiphertext, using: profileKey)
            } else {
                DeviceProfile.empty
            }
            return ManagedSIM(
                id: $0.id,
                numericID: $0.numericID,
                assignedDeviceID: $0.assignedDeviceID,
                assignedSlotID: $0.assignedSlotID,
                lastSeenAt: $0.lastSeenAt,
                lineName: profile.lineName,
                phoneNumber: profile.phoneNumber
            )
        }
    }

    func retireSIMCard(simID: UUID) async throws {
        let (_, response) = try await authenticatedRequest(path: "v1/sims/\(simID.uuidString.lowercased())", method: "DELETE")
        guard response.statusCode == 204 else { throw ServerConnectionError.requestFailed(response.statusCode) }
    }

    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState {
        let (data, response) = try await authenticatedRequest(
            path: "v1/device-discovery-session",
            method: "POST"
        )
        guard response.statusCode == 201 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(DeviceDiscoverySessionState.self, from: data)
    }

    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState {
        let (data, response) = try await authenticatedRequest(
            path: "v1/device-discovery-session",
            method: "GET"
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(DeviceDiscoverySessionState.self, from: data)
    }

    func closeDeviceDiscovery() async throws {
        let (_, response) = try await authenticatedRequest(
            path: "v1/device-discovery-session",
            method: "DELETE"
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func discoveredDevices() async throws -> [DiscoveredDevice] {
        let (data, response) = try await authenticatedRequest(
            path: "v1/device-discoveries",
            method: "GET"
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(DeviceDiscoveriesResponse.self, from: data).discoveries
    }

    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice {
        let (data, response) = try await authenticatedRequest(
            path: "v1/device-discoveries/\(id.uuidString.lowercased())/approve",
            method: "POST"
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(DeviceApprovalResponse.self, from: data).device
    }

    func rejectDiscoveredDevice(id: UUID) async throws {
        let (_, response) = try await authenticatedRequest(
            path: "v1/device-discoveries/\(id.uuidString.lowercased())",
            method: "DELETE"
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func updateDeviceNumericID(deviceID: UUID, numericID: Int) async throws {
        guard (1...99).contains(numericID) else {
            throw ServerConnectionError.invalidResponse
        }
        let body = try Self.encoder.encode(DeviceNumericIDRequest(numericID: numericID))
        let (_, response) = try await authenticatedRequest(
            path: "v1/devices/\(deviceID.uuidString.lowercased())/numeric-id",
            method: "PATCH",
            body: body
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func updateDeviceProfile(deviceID: UUID, profileCiphertext: Data) async throws {
        guard !profileCiphertext.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        let body = try Self.encoder.encode(
            DeviceProfileRequest(profileCiphertext: profileCiphertext)
        )
        let (_, response) = try await authenticatedRequest(
            path: "v1/devices/\(deviceID.uuidString.lowercased())/profile",
            method: "PUT",
            body: body
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func updateSIMNumericID(simID: UUID, numericID: Int) async throws {
        guard (1...99).contains(numericID) else {
            throw ServerConnectionError.invalidResponse
        }
        let body = try Self.encoder.encode(DeviceNumericIDRequest(numericID: numericID))
        let (_, response) = try await authenticatedRequest(
            path: "v1/sims/\(simID.uuidString.lowercased())/numeric-id",
            method: "PATCH",
            body: body
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func updateSIMProfile(simID: UUID, profileCiphertext: Data) async throws {
        guard !profileCiphertext.isEmpty else {
            throw ServerConnectionError.invalidResponse
        }
        let body = try Self.encoder.encode(
            DeviceProfileRequest(profileCiphertext: profileCiphertext)
        )
        let (_, response) = try await authenticatedRequest(
            path: "v1/sims/\(simID.uuidString.lowercased())/profile",
            method: "PUT",
            body: body
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func revokeDevice(deviceID: UUID) async throws {
        let (_, response) = try await authenticatedRequest(
            path: "v1/devices/\(deviceID.uuidString.lowercased())",
            method: "DELETE"
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    private static func isFresh(_ date: Date?, now: Date) -> Bool {
        guard let date else { return false }
        let age = now.timeIntervalSince(date)
        return age >= 0 && age < 60
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        guard let recipient = try await loadDevices().first(where: { $0.deviceID == deviceID }),
              recipient.isOnline,
              let publicKey = recipient.commandPublicKey,
              publicKey.count == 32 else {
            throw ServerConnectionError.deviceUnavailable
        }
        return publicKey
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
        let (data, response) = try await authenticatedRequest(
            path: "v1/media/turn-credentials",
            method: "GET"
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        let credentials = try Self.decoder.decode(CallTURNCredentials.self, from: data)
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

    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage {
        let (data, response) = try await authenticatedRequest(
            path: "v1/events",
            method: "GET",
            queryItems: [
                URLQueryItem(name: "after_sequence", value: String(afterSequence)),
                URLQueryItem(name: "limit", value: "100"),
            ]
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(EncryptedEventPage.self, from: data)
    }

    func openEncryptedEventStream(
        afterSequence: Int64
    ) async throws -> any EncryptedEventStream {
        guard afterSequence >= 0 else {
            throw ServerConnectionError.invalidResponse
        }
        let baseURL = try validatedBaseURL()
        let activeSession = try await validSession(baseURL: baseURL)
        let httpURL = baseURL.appending(path: "v1/events/ws")
        guard var components = URLComponents(url: httpURL, resolvingAgainstBaseURL: false) else {
            throw ServerConnectionError.invalidAddress
        }
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.queryItems = [
            URLQueryItem(name: "after_sequence", value: String(afterSequence)),
        ]
        guard let url = components.url else {
            throw ServerConnectionError.invalidAddress
        }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(activeSession.accessToken)", forHTTPHeaderField: "Authorization")
        let connection = webSocketFactory.makeConnection(for: request)
        connection.resume()
        return ServerEncryptedEventStream(
            connection: connection,
            afterSequence: afterSequence,
            decoder: Self.decoder,
            encoder: Self.encoder
        )
    }

    func registerAppInstallation(
        _ registration: AppInstallationRegistration
    ) async throws -> AppInstallationTrust {
        guard registration.identityPublicKey.count == 32,
              registration.keyAgreementPublicKey.count == 32 else {
            throw ServerConnectionError.invalidResponse
        }
        let body = try Self.encoder.encode(
            AppInstallationRegistrationRequest(
                installationID: registration.id,
                identityPublicKey: registration.identityPublicKey,
                keyAgreementPublicKey: registration.keyAgreementPublicKey,
                wrappedVaultKey: registration.wrappedVaultKey
            )
        )
        let (data, response) = try await authenticatedRequest(
            path: "v1/app-installations",
            method: "POST",
            body: body
        )
        guard response.statusCode == 200 || response.statusCode == 201 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(AppInstallationRegistrationResponse.self, from: data)
            .installation
    }

    func claimIncomingCall(
        notificationID: UUID,
        installationID: UUID
    ) async throws -> IncomingCallClaim {
        let body = try Self.encoder.encode(
            IncomingCallClaimRequest(installationID: installationID)
        )
        let (data, response) = try await authenticatedRequest(
            path: "v1/incoming-calls/\(notificationID.uuidString.lowercased())/claim",
            method: "POST",
            body: body
        )
        guard response.statusCode == 200 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
        return try Self.decoder.decode(IncomingCallClaimResponse.self, from: data).claim
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
        let body = try Self.encoder.encode(
            PushTokenRegistrationRequest(environment: environment.rawValue, token: token)
        )
        let (_, response) = try await authenticatedRequest(
            path: "v1/app-installations/\(installationID.uuidString.lowercased())/push-tokens/\(kind.rawValue)",
            method: "PUT",
            body: body
        )
        guard response.statusCode == 204 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    func unregisterPushToken(
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws {
        let (_, response) = try await authenticatedRequest(
            path: "v1/app-installations/\(installationID.uuidString.lowercased())/push-tokens/\(kind.rawValue)",
            method: "DELETE",
            queryItems: [URLQueryItem(name: "environment", value: environment.rawValue)]
        )
        guard response.statusCode == 204 || response.statusCode == 404 else {
            throw ServerConnectionError.requestFailed(response.statusCode)
        }
    }

    private func authenticatedRequest(
        path: String,
        method: String,
        body: Data? = nil,
        queryItems: [URLQueryItem] = []
    ) async throws -> (Data, HTTPURLResponse) {
        do {
            let baseURL = try validatedBaseURL()
            var activeSession = try await validSession(baseURL: baseURL)
            var urlRequest = request(
                baseURL: baseURL,
                path: path,
                method: method,
                body: body,
                queryItems: queryItems,
                accessToken: activeSession.accessToken
            )
            var result = try await network.data(for: urlRequest)
            if result.1.statusCode == 401 {
                Self.logger.notice("Access token rejected; refreshing session for \(method, privacy: .public) \(path, privacy: .public)")
                activeSession = try await refresh(baseURL: baseURL, session: activeSession)
                urlRequest = request(
                    baseURL: baseURL,
                    path: path,
                    method: method,
                    body: body,
                    queryItems: queryItems,
                    accessToken: activeSession.accessToken
                )
                result = try await network.data(for: urlRequest)
            }
            let requestID = result.1.value(forHTTPHeaderField: "X-Request-ID") ?? "missing"
            if result.1.statusCode >= 400 {
                Self.logger.error(
                    "Server request failed: \(method, privacy: .public) \(path, privacy: .public), status \(result.1.statusCode, privacy: .public), request ID \(requestID, privacy: .public)"
                )
            } else {
                Self.logger.info(
                    "Server request completed: \(method, privacy: .public) \(path, privacy: .public), status \(result.1.statusCode, privacy: .public), request ID \(requestID, privacy: .public)"
                )
            }
            return result
        } catch {
            state = .failed
            lastErrorDescription = error.localizedDescription
            Self.logger.error(
                "Server request transport failed: \(method, privacy: .public) \(path, privacy: .public), error \(String(reflecting: error), privacy: .public)"
            )
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
        var request = URLRequest(url: baseURL.appending(path: "v1/auth/refresh"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(RefreshRequest(refreshToken: session.refreshToken))
        let (data, response) = try await network.data(for: request)
        guard response.statusCode == 200 else {
            let requestID = response.value(forHTTPHeaderField: "X-Request-ID") ?? "missing"
            Self.logger.error("Session refresh failed with status \(response.statusCode, privacy: .public), request ID \(requestID, privacy: .public)")
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

    private func request(
        baseURL: URL,
        path: String,
        method: String,
        body: Data?,
        queryItems: [URLQueryItem],
        accessToken: String
    ) -> URLRequest {
        let url = baseURL.appending(path: path)
        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        components?.queryItems = queryItems.isEmpty ? nil : queryItems
        var request = URLRequest(url: components?.url ?? url)
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

extension ServerConnectionManager: EncryptedCallCommandAPI, CallTURNCredentialProviding {}
extension ServerConnectionManager: EncryptedEventStreamOpening {}
extension ServerConnectionManager: IncomingCallClaiming {}
extension ServerConnectionManager: PushTokenRegistering {}
extension ServerConnectionManager: DeviceDiscoveryAPI {}

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

private struct PasskeyOptionsResponse: Decodable {
    let ceremonyID: String
    let options: PasskeyOptions

    enum CodingKeys: String, CodingKey {
        case ceremonyID = "ceremony_id"
        case options
    }
}

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

private struct AppInstallationRegistrationRequest: Encodable {
    let installationID: UUID
    let identityPublicKey: Data
    let keyAgreementPublicKey: Data
    let wrappedVaultKey: Data?

    enum CodingKeys: String, CodingKey {
        case installationID = "installation_id"
        case identityPublicKey = "identity_public_key"
        case keyAgreementPublicKey = "key_agreement_public_key"
        case wrappedVaultKey = "wrapped_vault_key"
    }
}

private struct AppInstallationRegistrationResponse: Decodable {
    let installation: AppInstallationTrust
}

private struct IncomingCallClaimRequest: Encodable {
    let installationID: UUID

    enum CodingKeys: String, CodingKey {
        case installationID = "installation_id"
    }
}

private struct IncomingCallClaimResponse: Decodable {
    let claim: IncomingCallClaim
}

private struct PushTokenRegistrationRequest: Encodable {
    let environment: String
    let token: Data
}

private struct DeviceNumericIDRequest: Encodable {
    let numericID: Int

    enum CodingKeys: String, CodingKey {
        case numericID = "numeric_id"
    }
}

private struct DeviceProfileRequest: Encodable {
    let profileCiphertext: Data

    enum CodingKeys: String, CodingKey {
        case profileCiphertext = "profile_ciphertext"
    }
}

private struct DeviceListResponse: Decodable {
    let devices: [ServerDeviceRecord]
}

private struct SIMListResponse: Decodable {
    let sims: [ServerSIMRecord]
}

private struct ServerSIMRecord: Decodable {
    let id: UUID
    let numericID: Int
    let assignedDeviceID: UUID?
    let assignedSlotID: UUID?
    let lastSeenAt: Date
    let profileCiphertext: Data

    enum CodingKeys: String, CodingKey {
        case id = "sim_id"
        case numericID = "numeric_id"
        case assignedDeviceID = "assigned_device_uuid"
        case assignedSlotID = "assigned_slot_id"
        case lastSeenAt = "last_seen_at"
        case profileCiphertext = "profile_ciphertext"
    }

    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(UUID.self, forKey: .id)
        numericID = try values.decode(Int.self, forKey: .numericID)
        assignedDeviceID = try values.decodeIfPresent(UUID.self, forKey: .assignedDeviceID)
        assignedSlotID = try values.decodeIfPresent(UUID.self, forKey: .assignedSlotID)
        lastSeenAt = try values.decode(Date.self, forKey: .lastSeenAt)
        profileCiphertext = try values.decodeIfPresent(Data.self, forKey: .profileCiphertext) ?? Data()
    }
}

private struct DeviceDiscoveriesResponse: Decodable {
    let discoveries: [DiscoveredDevice]
}

private struct DeviceApprovalResponse: Decodable {
    let device: ApprovedDevice
}

private struct ServerDeviceRecord: Decodable {
    let deviceUUID: UUID
    let numericID: Int
    let profileCiphertext: Data?
    let identityPublicKey: Data
    let commandPublicKey: Data?
    let lastSeenAt: Date?
    let radioAccessTechnology: CellularRadioAccessTechnology?
    let signalBars: Int?
    let radioStatusUpdatedAt: Date?
    let slots: [ServerDeviceSlot]

    enum CodingKeys: String, CodingKey {
        case deviceUUID = "device_uuid"
        case numericID = "numeric_id"
        case profileCiphertext = "profile_ciphertext"
        case identityPublicKey = "identity_public_key"
        case commandPublicKey = "command_public_key"
        case lastSeenAt = "last_seen_at"
        case radioAccessTechnology = "radio_access_technology"
        case signalBars = "signal_bars"
        case radioStatusUpdatedAt = "radio_status_updated_at"
        case slots
    }

    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        deviceUUID = try values.decode(UUID.self, forKey: .deviceUUID)
        numericID = try values.decode(Int.self, forKey: .numericID)
        profileCiphertext = try values.decodeIfPresent(Data.self, forKey: .profileCiphertext)
        identityPublicKey = try values.decode(Data.self, forKey: .identityPublicKey)
        commandPublicKey = try values.decodeIfPresent(Data.self, forKey: .commandPublicKey)
        lastSeenAt = try values.decodeIfPresent(Date.self, forKey: .lastSeenAt)
        radioAccessTechnology = try values.decodeIfPresent(CellularRadioAccessTechnology.self, forKey: .radioAccessTechnology)
        signalBars = try values.decodeIfPresent(Int.self, forKey: .signalBars)
        radioStatusUpdatedAt = try values.decodeIfPresent(Date.self, forKey: .radioStatusUpdatedAt)
        slots = try values.decodeIfPresent([ServerDeviceSlot].self, forKey: .slots) ?? []
    }
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
