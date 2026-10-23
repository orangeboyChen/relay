// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import OSLog
import UIKit
import UserNotifications

extension Notification.Name {
    static let simDeckMessageNotificationPreferenceChanged = Notification.Name(
        "sim-deck.message-notification-preference-changed"
    )
}

@MainActor
final class AlertPushCoordinator: NSObject {
    private let logger = Logger(subsystem: "com.nowcent.simdeck", category: "AlertPushCoordinator")
    private let tokenRegistrar: any PushTokenRegistering
    private let environment: PushEnvironment
    private let requestRemoteNotificationRegistration: @MainActor () async -> Void
    private var installationID: UUID?
    private var pendingToken: Data?
    private var requiresTokenDeletion = false
    private var tokenObserver: NSObjectProtocol?
    private var preferenceObserver: NSObjectProtocol?
    private var hasStarted = false
    private var isEnabled: Bool?

    init(
        tokenRegistrar: any PushTokenRegistering,
        environment: PushEnvironment,
        requestRemoteNotificationRegistration: @escaping @MainActor () async -> Void = {
            let center = UNUserNotificationCenter.current()
            let granted = (try? await center.requestAuthorization(options: [.alert, .badge, .sound])) ?? false
            guard granted else { return }
            UIApplication.shared.registerForRemoteNotifications()
        }
    ) {
        self.tokenRegistrar = tokenRegistrar
        self.environment = environment
        self.requestRemoteNotificationRegistration = requestRemoteNotificationRegistration
    }

    func start(installationID: UUID) {
        configure(installationID: installationID)
        guard !hasStarted else { return }
        hasStarted = true
        tokenObserver = NotificationCenter.default.addObserver(
            forName: .simDeckAPNsToken, object: nil, queue: .main
        ) { [weak self] notification in
            guard let token = notification.object as? Data else { return }
            Task { @MainActor [weak self] in
                do {
                    try await self?.receiveToken(token)
                } catch {
                    self?.logger.error(
                        "Unable to register alert push token: \(error.localizedDescription, privacy: .public)"
                    )
                }
            }
        }
        preferenceObserver = NotificationCenter.default.addObserver(
            forName: .simDeckMessageNotificationPreferenceChanged,
            object: nil,
            queue: .main
        ) { [weak self] notification in
            guard let enabled = notification.object as? Bool else { return }
            Task { @MainActor [weak self] in
                do {
                    try await self?.setEnabled(enabled)
                } catch {
                    self?.logger.error(
                        "Unable to update alert push preference: \(error.localizedDescription, privacy: .public)"
                    )
                }
            }
        }
        let center = UNUserNotificationCenter.current()
        let copyCodeAction = UNNotificationAction(
            identifier: AlertNotificationMetadata.copyCodeActionIdentifier,
            title: String(localized: "Copy Code"),
            options: [.authenticationRequired]
        )
        center.setNotificationCategories([
            UNNotificationCategory(
                identifier: AlertNotificationMetadata.otpCategoryIdentifier,
                actions: [copyCodeAction],
                intentIdentifiers: [],
                options: []
            )
        ])
        Task {
            try? await setEnabled(NotificationPreferences().messageNotifications)
            try? await uploadPendingToken()
        }
    }

    func configure(installationID: UUID) {
        self.installationID = installationID
    }

    func setEnabled(_ enabled: Bool) async throws {
        guard isEnabled != enabled else { return }
        isEnabled = enabled
        guard enabled else {
            pendingToken = nil
            requiresTokenDeletion = true
            try await uploadPendingToken()
            return
        }
        await requestRemoteNotificationRegistration()
    }

    func receiveToken(_ token: Data) async throws {
        guard isEnabled == true else { return }
        pendingToken = token
        try await uploadPendingToken()
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

    func stop() async throws {
        if let tokenObserver {
            NotificationCenter.default.removeObserver(tokenObserver)
            self.tokenObserver = nil
        }
        if let preferenceObserver {
            NotificationCenter.default.removeObserver(preferenceObserver)
            self.preferenceObserver = nil
        }
        hasStarted = false
        isEnabled = false
        pendingToken = nil
        requiresTokenDeletion = true
        try await uploadPendingToken()
    }

}

extension Notification.Name {
    static let simDeckAPNsToken = Notification.Name("sim-deck.apns-token")
}
