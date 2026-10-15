import SwiftUI

@main
struct SimDeckApp: App {
    @State private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            RootTabView(model: model)
                .task {
                    model.bootstrapInstallation()
                }
        }
    }
}
