import Foundation
import SwiftUI

@main
struct SimDeckApp: App {
    @State private var model: AppModel

    init() {
#if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--preview-fixtures") {
            _model = State(initialValue: .preview(selectedTab: .messages))
        } else {
            _model = State(initialValue: AppModel())
        }
#else
        _model = State(initialValue: AppModel())
#endif
    }

    var body: some Scene {
        WindowGroup {
            RootTabView(model: model)
                .task {
                    model.bootstrapInstallation()
                    await model.refreshDevices()
                }
        }
    }
}
