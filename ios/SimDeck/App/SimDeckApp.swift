// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI
import UIKit
import UserNotifications

final class SimDeckAppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    private var onRemoteNotificationActivity: (@MainActor () async -> Void)?
    private var onMessageNotificationSelection: (@MainActor (String) async -> Void)?
    private var hasPendingRemoteNotification = false
    private var pendingMessageNotificationSelections: [String] = []

    @MainActor
    func setRemoteNotificationHandlers(
        onActivity: @escaping @MainActor () async -> Void,
        onMessageSelection: @escaping @MainActor (String) async -> Void
    ) {
        onRemoteNotificationActivity = onActivity
        self.onMessageNotificationSelection = onMessageSelection
        guard hasPendingRemoteNotification || !pendingMessageNotificationSelections.isEmpty else {
            return
        }
        hasPendingRemoteNotification = false
        let selections = pendingMessageNotificationSelections
        pendingMessageNotificationSelections.removeAll()
        Task {
            await onActivity()
            for identifier in selections {
                await onMessageSelection(identifier)
            }
        }
    }

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        NotificationCenter.default.post(name: .simDeckAPNsToken, object: deviceToken)
    }

    func application(
        _ application: UIApplication,
        didReceiveRemoteNotification userInfo: [AnyHashable: Any],
        fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void
    ) {
        let completion = BackgroundFetchCompletion(completionHandler)
        Task { @MainActor [weak self] in
            if let handler = self?.onRemoteNotificationActivity {
                await handler()
            } else {
                self?.hasPendingRemoteNotification = true
            }
            completion.finish(.newData)
        }
    }

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        Task { @MainActor [weak self] in
            await self?.onRemoteNotificationActivity?()
        }
        guard NotificationPreferences().messageNotifications else {
            completionHandler([])
            return
        }
        let conversationIdentifier = notification.request.content.threadIdentifier
        if !conversationIdentifier.isEmpty,
           MessageNotificationMuteStore.isMuted(
               conversationIdentifier: conversationIdentifier
           ) {
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
        let content = response.notification.request.content
        let actionIdentifier = response.actionIdentifier
        let copiedCode = actionIdentifier == AlertNotificationMetadata.copyCodeActionIdentifier
            ? content.userInfo[AlertNotificationMetadata.otpUserInfoKey] as? String
            : nil
        let conversationIdentifier = AlertNotificationMetadata.conversationIdentifier(
            content: content,
            actionIdentifier: actionIdentifier
        )
        Task { @MainActor [weak self] in
            guard let self else { return }
            if let copiedCode { UIPasteboard.general.string = copiedCode }
            if let activity = onRemoteNotificationActivity {
                await activity()
                if let conversationIdentifier,
                   let selection = onMessageNotificationSelection {
                    await selection(conversationIdentifier)
                }
            } else {
                hasPendingRemoteNotification = true
                if let conversationIdentifier {
                    pendingMessageNotificationSelections.append(conversationIdentifier)
                }
            }
        }
    }
}

private final class BackgroundFetchCompletion: @unchecked Sendable {
    private let handler: (UIBackgroundFetchResult) -> Void

    init(_ handler: @escaping (UIBackgroundFetchResult) -> Void) {
        self.handler = handler
    }

    func finish(_ result: UIBackgroundFetchResult) {
        handler(result)
    }
}

@main
struct SimDeckApp: App {
    @UIApplicationDelegateAdaptor(SimDeckAppDelegate.self) private var appDelegate
    @Environment(\.scenePhase) private var scenePhase
    @State private var model: AppModel
    private let shouldStartLiveServices: Bool

    init() {
#if DEBUG
        let liveServicesEnabled = LiveServiceLaunchPolicy.shouldStart(
            arguments: ProcessInfo.processInfo.arguments,
            environment: ProcessInfo.processInfo.environment
        )
        if ProcessInfo.processInfo.arguments.contains("--preview-active-call") {
            _model = State(initialValue: .preview(activeCall: PreviewFixtures.activeCall))
            shouldStartLiveServices = false
        } else if ProcessInfo.processInfo.arguments.contains("--preview-onboarding") {
            _model = State(initialValue: .preview(onboardingCompleted: false))
            shouldStartLiveServices = false
        } else if ProcessInfo.processInfo.arguments.contains("--preview-profile") {
            _model = State(initialValue: .preview(selectedTab: .profile))
            shouldStartLiveServices = false
        } else if ProcessInfo.processInfo.arguments.contains("--preview-fixtures") {
            _model = State(initialValue: .preview(selectedTab: .messages))
            shouldStartLiveServices = false
        } else {
            _model = State(initialValue: AppModel())
            shouldStartLiveServices = liveServicesEnabled
        }
#else
        _model = State(initialValue: AppModel())
        shouldStartLiveServices = true
#endif
    }

    var body: some Scene {
        WindowGroup {
            Group {
                if model.isOnboardingPresented {
                    OnboardingScreen(model: model)
                } else {
                    RootTabView(model: model)
                }
            }
                .task {
                    NotificationPreferences.migrateToSharedDefaults()
                    model.refreshContactNames()
                    model.startContactUpdates()
                    appDelegate.setRemoteNotificationHandlers(
                        onActivity: {
                            await model.refreshMessages()
                        },
                        onMessageSelection: { conversationIdentifier in
                            await model.handleMessageNotificationSelection(
                                conversationIdentifier: conversationIdentifier
                            )
                        }
                    )
                    guard shouldStartLiveServices else { return }
                    model.startNetworkPathObservation()
                    model.bootstrapInstallation()
                    model.startVoIPPushes()
                    await model.refreshDevices()
                    await model.refreshMessages()
                    model.startMessageRealtimeUpdates()
                }
                .onChange(of: scenePhase) { _, phase in
                    guard shouldStartLiveServices else { return }
                    if phase == .active {
                        Task {
                            await model.refreshForegroundState()
                            model.startMessageRealtimeUpdates()
                        }
                    } else {
                        model.stopMessageRealtimeUpdates()
                    }
                }
        }
    }
}

enum LiveServiceLaunchPolicy {
    private static let previewArguments: Set<String> = [
        "--preview-onboarding",
        "--preview-profile",
        "--preview-fixtures",
        "--preview-active-call",
    ]

    static func shouldStart(arguments: [String], environment: [String: String]) -> Bool {
        guard environment["XCTestConfigurationFilePath"] == nil else { return false }
        return previewArguments.isDisjoint(with: arguments)
    }
}
