import SwiftUI

struct RootTabView: View {
    @Bindable var model: AppModel

    var body: some View {
        TabView(selection: $model.selectedTab) {
            NavigationStack {
                PlaceholderScreen(
                    title: "Calls",
                    systemImage: "phone.fill"
                )
            }
            .tabItem {
                Label("Calls", systemImage: "phone.fill")
            }
            .tag(AppTab.calls)

            NavigationStack {
                PlaceholderScreen(
                    title: "Messages",
                    systemImage: "message.fill"
                )
            }
            .tabItem {
                Label("Messages", systemImage: "message.fill")
            }
            .tag(AppTab.messages)

            NavigationStack {
                PlaceholderScreen(
                    title: "Me",
                    systemImage: "person.crop.circle.fill"
                )
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
