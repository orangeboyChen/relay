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
        .fullScreenCover(item: activeCall) { _ in
            InCallScreen(
                coordinator: model.callCoordinator,
                lines: model.devices,
                referenceDate: nil
            )
        }
    }

    private var activeCall: Binding<ActiveCallSession?> {
        Binding(
            get: { model.callCoordinator.activeCall },
            set: { _ in }
        )
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
