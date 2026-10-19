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
    private let onNotificationActivity: @MainActor () async -> Void
    private var hasStarted = false
    init(
        tokenRegistrar: any PushTokenRegistering,
        environment: PushEnvironment,
        onNotificationActivity: @escaping @MainActor () async -> Void = {}
    ) {
        self.tokenRegistrar = tokenRegistrar
        self.environment = environment
        self.onNotificationActivity = onNotificationActivity
    }

    func start(installationID: UUID) {
        self.installationID = installationID
        guard !hasStarted else { return }
        hasStarted = true
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
        let copyCodeAction = UNNotificationAction(
            identifier: Self.copyCodeActionIdentifier,
            title: String(localized: "Copy Code"),
            options: [.authenticationRequired]
        )
        center.setNotificationCategories([
            UNNotificationCategory(
                identifier: Self.otpCategoryIdentifier,
                actions: [copyCodeAction],
                intentIdentifiers: [],
                options: []
            )
        ])
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
        Task { @MainActor in
            await self.onNotificationActivity()
        }
        guard NotificationPreferences().messageNotifications else {
            completionHandler([])
            return
        }
        completionHandler([.banner, .sound, .badge])
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        completionHandler()
        if response.actionIdentifier == Self.copyCodeActionIdentifier,
           let code = response.notification.request.content.userInfo[Self.otpUserInfoKey] as? String {
            UIPasteboard.general.string = code
        }
        Task { @MainActor in
            await self.onNotificationActivity()
        }
    }
}

private extension AlertPushCoordinator {
    nonisolated static let otpCategoryIdentifier = "simdeck.sms.otp"
    nonisolated static let copyCodeActionIdentifier = "simdeck.sms.copy-code"
    nonisolated static let otpUserInfoKey = "simdeck.sms.otp"
}

extension Notification.Name {
    static let simDeckAPNsToken = Notification.Name("sim-deck.apns-token")
}
