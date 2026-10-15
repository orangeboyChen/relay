import SwiftUI

struct RootTabView: View {
    @Bindable var model: AppModel

    var body: some View {
        TabView(selection: $model.selectedTab) {
            NavigationStack {
                CallsTabScreen(model: model)
            }
            .tabItem {
                Label("Calls", systemImage: "phone.fill")
            }
            .tag(AppTab.calls)

            NavigationStack {
                MessagesScreen(state: model.messages, lines: model.devices)
            }
            .tabItem {
                Label("Messages", systemImage: "message.fill")
            }
            .tag(AppTab.messages)

            NavigationStack {
                ProfileScreen(devices: model.devices, keyRecoveryState: model.keyRecoveryState)
            }
            .tabItem {
                Label("Me", systemImage: "person.crop.circle.fill")
            }
            .tag(AppTab.profile)
        }
    }
}

private struct PlaceholderScreen: View {
    let title: LocalizedStringKey
    let systemImage: String

    var body: some View {
        ContentUnavailableView(
            title,
            systemImage: systemImage,
            description: Text("This section is ready for its feature implementation.")
        )
        .navigationTitle(title)
    }
}

#if DEBUG
#Preview("Calls") {
    RootTabView(model: .preview())
}

#Preview("Messages - Dark") {
    RootTabView(model: .preview(selectedTab: .messages))
        .preferredColorScheme(.dark)
}

#Preview("Me - Accessibility Text") {
    RootTabView(model: .preview(selectedTab: .profile))
        .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
