import Foundation
import SwiftUI
import UIKit

final class SimDeckAppDelegate: NSObject, UIApplicationDelegate {
    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        NotificationCenter.default.post(name: .simDeckAPNsToken, object: deviceToken)
    }
}

@main
struct SimDeckApp: App {
    @UIApplicationDelegateAdaptor(SimDeckAppDelegate.self) private var appDelegate
    @State private var model: AppModel
    private let shouldStartLiveServices: Bool

    init() {
#if DEBUG
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
            shouldStartLiveServices = true
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
                    guard shouldStartLiveServices else { return }
                    model.bootstrapInstallation()
                    model.startVoIPPushes()
                    await model.refreshDevices()
                    await model.refreshMessages()
                }
        }
    }
}
