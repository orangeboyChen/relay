// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Intents
import SwiftUI

struct RootTabView: View {
    @Bindable var model: AppModel

    var body: some View {
        ZStack {
            TabView(selection: $model.selectedTab) {
                NavigationStack {
                    CallsTabScreen(model: model)
                }
                .tabItem {
                    Label("Calls", systemImage: "phone.fill")
                }
                .tag(AppTab.calls)

                NavigationStack {
                    MessagesScreen(
                        state: model.messages,
                        lines: model.devices,
                        defaultLineID: model.defaultMessageLineID,
                        refreshAction: {
                            await model.refreshMessages()
                        },
                        sendAction: { lineID, remoteNumber, body in
                            try await model.sendMessage(
                                lineID: lineID,
                                remoteNumber: remoteNumber,
                                body: body
                            )
                        },
                        retryAction: { messageID in
                            try await model.retryMessage(id: messageID)
                        },
                        deleteAction: { messageID in
                            try model.deleteMessage(id: messageID)
                        },
                        markReadAction: { conversationID in
                            model.markConversationRead(conversationID)
                        }
                    )
                }
                .tabItem {
                    Label("Messages", systemImage: "message.fill")
                }
                .tag(AppTab.messages)

                NavigationStack {
                    ContactsScreen(model: model)
                }
                .tabItem {
                    Label("Contacts", systemImage: "person.2.fill")
                }
                .tag(AppTab.contacts)

                NavigationStack {
                    ProfileScreen(model: model)
                }
                .tabItem {
                    Label("Me", systemImage: "person.crop.circle.fill")
                }
                .tag(AppTab.profile)
            }

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

#Preview("Contacts") {
    RootTabView(model: .preview(selectedTab: .contacts))
}

#Preview("Me - Accessibility Text") {
    RootTabView(model: .preview(selectedTab: .profile))
        .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
