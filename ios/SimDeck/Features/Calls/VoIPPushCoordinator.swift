import Foundation
import PushKit

@MainActor
final class VoIPPushCoordinator: NSObject {
    private let tokenRegistrar: any PushTokenRegistering
    private let environment: PushEnvironment
    private let incomingPushHandler: @MainActor (Data) async throws -> Void
    private var installationID: UUID?
    private var pendingToken: Data?
    private var requiresTokenDeletion = false
    private var registry: PKPushRegistry?

    init(
        tokenRegistrar: any PushTokenRegistering,
        environment: PushEnvironment,
        incomingPushHandler: @escaping @MainActor (Data) async throws -> Void
    ) {
        self.tokenRegistrar = tokenRegistrar
        self.environment = environment
        self.incomingPushHandler = incomingPushHandler
    }

    func start(installationID: UUID) {
        configure(installationID: installationID)
        guard registry == nil else { return }
        let registry = PKPushRegistry(queue: .main)
        registry.delegate = self
        registry.desiredPushTypes = [.voIP]
        self.registry = registry
    }

    func configure(installationID: UUID) {
        self.installationID = installationID
    }

    func receiveToken(_ token: Data) async throws {
        pendingToken = token
        try await uploadPendingToken()
    }

    func uploadPendingToken() async throws {
        guard let installationID else { return }
        if requiresTokenDeletion {
            try await tokenRegistrar.unregisterPushToken(
                kind: .voip,
                environment: environment,
                installationID: installationID
            )
            requiresTokenDeletion = false
        }
        guard let pendingToken else { return }
        try await tokenRegistrar.registerPushToken(
            pendingToken,
            kind: .voip,
            environment: environment,
            installationID: installationID
        )
        self.pendingToken = nil
    }

    func invalidateToken() async throws {
        pendingToken = nil
        requiresTokenDeletion = true
        try await uploadPendingToken()
    }

    func receiveIncomingPayload(_ dictionary: [AnyHashable: Any]) async throws {
        guard JSONSerialization.isValidJSONObject(dictionary) else {
            throw VoIPPushError.invalidPayload
        }
        let data = try JSONSerialization.data(withJSONObject: dictionary, options: [.sortedKeys])
        try await incomingPushHandler(data)
    }
}

extension VoIPPushCoordinator: @preconcurrency PKPushRegistryDelegate {
    func pushRegistry(
        _ registry: PKPushRegistry,
        didUpdate pushCredentials: PKPushCredentials,
        for type: PKPushType
    ) {
        guard type == .voIP else { return }
        Task { try? await receiveToken(pushCredentials.token) }
    }

    func pushRegistry(
        _ registry: PKPushRegistry,
        didReceiveIncomingPushWith payload: PKPushPayload,
        for type: PKPushType,
        completion: @escaping @Sendable () -> Void
    ) {
        guard type == .voIP else {
            completion()
            return
        }
        Task {
            defer { completion() }
            try? await receiveIncomingPayload(payload.dictionaryPayload)
        }
    }

    func pushRegistry(_ registry: PKPushRegistry, didInvalidatePushTokenFor type: PKPushType) {
        guard type == .voIP else { return }
        Task { try? await invalidateToken() }
    }
}

enum VoIPPushError: Error, Equatable {
    case invalidPayload
}
