import Foundation
import SwiftUI
import UIKit

final class SimDeckAppDelegate: NSObject, UIApplicationDelegate {
    private var onRemoteNotificationActivity: (@MainActor () async -> Void)?
    private var hasPendingRemoteNotification = false

    @MainActor
    func setRemoteNotificationHandler(_ handler: @escaping @MainActor () async -> Void) {
        onRemoteNotificationActivity = handler
        guard hasPendingRemoteNotification else { return }
        hasPendingRemoteNotification = false
        Task { await handler() }
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
        if ProcessInfo.processInfo.arguments.contains("--preview-onboarding") {
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
                    appDelegate.setRemoteNotificationHandler {
                        await model.refreshMessages()
                    }
                    guard shouldStartLiveServices else { return }
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
    ]

    static func shouldStart(arguments: [String], environment: [String: String]) -> Bool {
        guard environment["XCTestConfigurationFilePath"] == nil else { return false }
        return previewArguments.isDisjoint(with: arguments)
    }
}
