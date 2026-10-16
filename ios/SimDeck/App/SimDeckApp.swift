import Foundation
import SwiftUI

@main
struct SimDeckApp: App {
    @State private var model: AppModel
    private let shouldStartLiveServices: Bool

    init() {
#if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--preview-profile") {
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
            RootTabView(model: model)
                .task {
                    guard shouldStartLiveServices else { return }
                    model.bootstrapInstallation()
                    model.startVoIPPushes()
                    await model.refreshDevices()
                }
        }
    }
}
