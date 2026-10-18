import Foundation
import UIKit
import UserNotifications

@MainActor
final class AlertPushCoordinator: NSObject {
    private let tokenRegistrar: any PushTokenRegistering
    private let environment: PushEnvironment
    private var installationID: UUID?
    private var pendingToken: Data?
    private var requiresTokenDeletion = false
    private var observer: NSObjectProtocol?

    init(tokenRegistrar: any PushTokenRegistering, environment: PushEnvironment) {
        self.tokenRegistrar = tokenRegistrar
        self.environment = environment
    }

    func start(installationID: UUID) {
        self.installationID = installationID
        observer = NotificationCenter.default.addObserver(
            forName: .simDeckAPNsToken, object: nil, queue: .main
        ) { [weak self] notification in
            guard let token = notification.object as? Data else { return }
            Task { @MainActor [weak self] in
                self?.pendingToken = token
                try? await self?.uploadPendingToken()
            }
        }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .badge, .sound]) { granted, _ in
            guard granted else { return }
            DispatchQueue.main.async { UIApplication.shared.registerForRemoteNotifications() }
        }
    }

    func uploadPendingToken() async throws {
        guard let installationID else { return }
        if requiresTokenDeletion {
            try await tokenRegistrar.unregisterPushToken(
                kind: .alert, environment: environment, installationID: installationID
            )
            requiresTokenDeletion = false
        }
        guard let pendingToken else { return }
        try await tokenRegistrar.registerPushToken(
            pendingToken, kind: .alert, environment: environment, installationID: installationID
        )
        self.pendingToken = nil
    }

    func invalidateToken() async throws {
        pendingToken = nil
        requiresTokenDeletion = true
        try await uploadPendingToken()
    }
}

extension AlertPushCoordinator: UNUserNotificationCenterDelegate {
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound, .badge])
    }
}

extension Notification.Name {
    static let simDeckAPNsToken = Notification.Name("sim-deck.apns-token")
}
