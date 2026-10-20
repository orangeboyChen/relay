import Foundation
import OSLog
import PushKit

extension Notification.Name {
    static let simDeckIncomingCallPreferenceChanged = Notification.Name("sim-deck.incoming-call-preference-changed")
}

@MainActor
final class VoIPPushCoordinator: NSObject {
    private let logger = Logger(subsystem: "com.nowcent.simdeck", category: "VoIPPushCoordinator")
    private let tokenRegistrar: any PushTokenRegistering
    private let environment: PushEnvironment
    private let incomingPushHandler: @MainActor (Data) async throws -> Void
    private var installationID: UUID?
    private var pendingToken: Data?
    private var requiresTokenDeletion = false
    private var registry: PKPushRegistry?
    private var preferenceObserver: NSObjectProtocol?
    private var isEnabled = true

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
        if preferenceObserver == nil {
            preferenceObserver = NotificationCenter.default.addObserver(
                forName: .simDeckIncomingCallPreferenceChanged,
                object: nil,
                queue: .main
            ) { [weak self] notification in
                guard let enabled = notification.object as? Bool else { return }
                Task { @MainActor [weak self] in
                    try? await self?.setEnabled(enabled)
                }
            }
        }
        Task {
            do {
                try await setEnabled(NotificationPreferences().incomingCallNotifications)
                try await uploadPendingToken()
            } catch {
                logger.error(
                    "Unable to start VoIP push registration: \(String(reflecting: error), privacy: .public)"
                )
            }
        }
    }

    func setEnabled(_ enabled: Bool) async throws {
        guard isEnabled != enabled || (enabled && registry == nil) else { return }
        isEnabled = enabled
        guard enabled else {
            registry?.desiredPushTypes = []
            registry = nil
            pendingToken = nil
            requiresTokenDeletion = true
            try await uploadPendingToken()
            return
        }
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
        guard isEnabled else { return }
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

    func stop() async throws {
        if let preferenceObserver {
            NotificationCenter.default.removeObserver(preferenceObserver)
            self.preferenceObserver = nil
        }
        registry?.desiredPushTypes = []
        registry = nil
        isEnabled = false
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
        Task {
            do {
                try await receiveToken(pushCredentials.token)
                logger.info("VoIP push token registered with the server")
            } catch {
                logger.error(
                    "Unable to register VoIP push token: \(String(reflecting: error), privacy: .public)"
                )
            }
        }
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
            do {
                try await receiveIncomingPayload(payload.dictionaryPayload)
                logger.info("Incoming VoIP push reported to CallKit")
            } catch {
                logger.error(
                    "Unable to process incoming VoIP push: \(String(reflecting: error), privacy: .public)"
                )
            }
        }
    }

    func pushRegistry(_ registry: PKPushRegistry, didInvalidatePushTokenFor type: PKPushType) {
        guard type == .voIP else { return }
        Task {
            do {
                try await invalidateToken()
            } catch {
                logger.error(
                    "Unable to remove invalidated VoIP push token: \(String(reflecting: error), privacy: .public)"
                )
            }
        }
    }
}

enum VoIPPushError: Error, Equatable {
    case invalidPayload
}
