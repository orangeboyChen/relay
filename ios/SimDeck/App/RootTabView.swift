// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Intents
import SwiftUI

struct RootTabView: View {
    @Bindable var model: AppModel

    var body: some View {
        ZStack {
            tabs

            if model.callCoordinator.activeCall != nil {
                InCallScreen(
                    coordinator: model.callCoordinator,
                    lines: model.devices,
                    referenceDate: nil
                )
                .transition(.opacity)
                .zIndex(1)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: model.callCoordinator.activeCall != nil)
        .onContinueUserActivity(INStartCallIntentIdentifier) { activity in
            Task {
                await model.handleDefaultCallingActivity(activity)
            }
        }
        .onOpenURL { url in
            guard let request = DefaultCallingRequestParser.request(from: url) else { return }
            Task {
                await model.handleDefaultCallingRequest(request)
            }
        }
    }

    @ViewBuilder
    private var tabs: some View {
        if #available(iOS 27.0, *) {
            TabView(selection: $model.selectedTab) {
                standardTabs

                #if compiler(>=6.4)
                Tab(
                    "Keypad",
                    systemImage: "circle.grid.3x3.fill",
                    value: AppTab.keypad,
                    role: .prominent
                ) {
                    keypadContent
                }
                #else
                Tab("Keypad", systemImage: "circle.grid.3x3.fill", value: AppTab.keypad) {
                    keypadContent
                }
                #endif
            }
            .tabViewStyle(.tabBarOnly)
        } else {
            TabView(selection: $model.selectedTab) {
                standardTabs

                Tab("Keypad", systemImage: "circle.grid.3x3.fill", value: AppTab.keypad) {
                    keypadContent
                }
            }
            .tabViewStyle(.tabBarOnly)
        }
    }

    @TabContentBuilder<AppTab>
    private var standardTabs: some TabContent<AppTab> {
        Tab("Calls", systemImage: "phone.fill", value: AppTab.calls) {
            NavigationStack {
                CallsTabScreen(model: model)
            }
        }

        Tab("Messages", systemImage: "message.fill", value: AppTab.messages) {
            NavigationStack {
                MessagesTabScreen(model: model)
            }
        }

        Tab("Contacts", systemImage: "person.2.fill", value: AppTab.contacts) {
            NavigationStack {
                ContactsScreen(model: model)
            }
        }

        Tab("Me", systemImage: "person.crop.circle.fill", value: AppTab.profile) {
            NavigationStack {
                ProfileScreen(model: model)
            }
        }
    }

    private var keypadContent: some View {
        NavigationStack {
            KeypadTabScreen(model: model)
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
        .scrollEdgeEffectStyle(.soft, for: .all)
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

#Preview("Contacts") {
    RootTabView(model: .preview(selectedTab: .contacts))
}

#Preview("Me - Accessibility Text") {
    RootTabView(model: .preview(selectedTab: .profile))
        .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("Keypad") {
    RootTabView(model: .preview(selectedTab: .keypad))
}
#endif
