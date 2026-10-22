// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Intents
import SwiftUI

struct RootTabView: View {
    @Bindable var model: AppModel

    @State private var navigationPath = NavigationPath()
    @State private var callEditMode: EditMode = .inactive
    @State private var callFilter: CallHistoryFilter = .all
    @State private var callLineID: UUID?
    @State private var messageSearchText = ""
    @State private var messageLineID: UUID?
    @State private var messageEditMode: EditMode = .inactive
    @State private var contactSearchText = ""
    @State private var isMessageComposerPresented = false

    var body: some View {
        navigationContainer
            .sheet(isPresented: $isMessageComposerPresented) {
                NewMessageScreen(
                    lines: model.devices,
                    selectedLineID: model.defaultMessageLineID,
                    sendAction: { lineID, number, body in
                        try await model.sendMessage(
                            lineID: lineID,
                            remoteNumber: number,
                            body: body
                        )
                    },
                    sendImageAction: { lineID, number, body, attachment in
                        try await model.sendImageMessage(
                            lineID: lineID,
                            remoteNumber: number,
                            body: body,
                            attachment: attachment
                        )
                    }
                )
            }
            .onChange(of: model.selectedTab) { _, _ in
                callEditMode = .inactive
                messageEditMode = .inactive
            }
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

    private var navigationContainer: some View {
        NavigationStack(path: $navigationPath) {
            rootContent
            .navigationTitle(rootTitle)
            .navigationBarTitleDisplayMode(rootTitleDisplayMode)
            .toolbar {
                if navigationPath.isEmpty {
                    rootToolbar
                }
            }
            .navigationDestination(for: CallDetailRoute.self) { route in
                callDetailDestination(for: route)
            }
            .navigationDestination(for: ConversationID.self) { conversationID in
                conversationDestination(for: conversationID)
            }
            .navigationDestination(for: ContactRecord.self) { contact in
                contactDetailDestination(for: contact)
            }
        }
    }

    private var rootContent: some View {
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
            CallsTabScreen(
                model: model,
                historyEditMode: $callEditMode,
                filter: $callFilter,
                selectedLineID: $callLineID
            )
        }

        Tab("Messages", systemImage: "message.fill", value: AppTab.messages) {
            MessagesTabScreen(
                model: model,
                searchText: $messageSearchText,
                selectedLineID: $messageLineID,
                editMode: $messageEditMode
            )
        }

        Tab("Contacts", systemImage: "person.2.fill", value: AppTab.contacts) {
            ContactsScreen(model: model, searchText: $contactSearchText)
        }

        Tab("Me", systemImage: "person.crop.circle.fill", value: AppTab.profile) {
            ProfileScreen(model: model)
        }
    }

    private var keypadContent: some View {
        KeypadTabScreen(model: model)
    }

    private var rootTitle: LocalizedStringKey {
        switch model.selectedTab {
        case .calls:
            "Calls"
        case .messages:
            "Messages"
        case .contacts:
            "Contacts"
        case .profile:
            "Me"
        case .keypad:
            ""
        }
    }

    private var rootTitleDisplayMode: NavigationBarItem.TitleDisplayMode {
        switch model.selectedTab {
        case .calls, .messages:
            .inline
        case .contacts, .profile:
            .large
        case .keypad:
            .inline
        }
    }

    @ToolbarContentBuilder
    private var rootToolbar: some ToolbarContent {
        if model.selectedTab == .calls {
            if hasCallHistory {
                ToolbarItem(placement: .topBarLeading) {
                    Button(callEditMode.isEditing ? "Done" : "Edit") {
                        withAnimation {
                            callEditMode = callEditMode.isEditing ? .inactive : .active
                        }
                    }
                }

                if !callEditMode.isEditing {
                    ToolbarItem(placement: .topBarTrailing) {
                        callFilterMenu
                    }
                }
            }
        }

        if model.selectedTab == .messages {
            if hasMessages {
                ToolbarItem(placement: .topBarLeading) {
                    Button(messageEditMode.isEditing ? "Done" : "Edit") {
                        withAnimation(.snappy) {
                            messageEditMode = messageEditMode.isEditing ? .inactive : .active
                        }
                    }
                    .controlSize(.large)
                }
            }

            if !messageEditMode.isEditing {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    SIMFilterMenu(lines: model.devices, selection: $messageLineID)

                    Button {
                        isMessageComposerPresented = true
                    } label: {
                        Label("New Message", systemImage: "square.and.pencil")
                            .labelStyle(.iconOnly)
                    }
                    .disabled(!hasOnlineLine)
                    .accessibilityHint(
                        hasOnlineLine ? Text("") : Text("An online SIM is required.")
                    )
                }
            }
        }

    }

    private var callFilterMenu: some View {
        Menu {
            Picker("Call filter", selection: $callFilter) {
                Label("Calls", systemImage: "phone")
                    .tag(CallHistoryFilter.all)
                Label("Missed", systemImage: "phone.down")
                    .tag(CallHistoryFilter.missed)
                Label(
                    "Unknown Callers",
                    systemImage: "person.crop.circle.badge.questionmark"
                )
                .tag(CallHistoryFilter.unknownCallers)
            }
            .pickerStyle(.inline)

            Menu {
                Picker("SIM", selection: $callLineID) {
                    Label("All SIMs", systemImage: "simcard.2")
                        .tag(nil as UUID?)

                    ForEach(model.devices) { line in
                        SIMLabel(line: line)
                            .tag(line.id as UUID?)
                    }
                }
            } label: {
                if let selectedCallLine {
                    HStack {
                        SIMBadge(line: selectedCallLine, size: 16)
                        Text(selectedCallLine.displayName)
                    }
                } else {
                    Label("All SIMs", systemImage: "simcard.2")
                }
            }
        } label: {
            Label(
                "Call filter",
                systemImage: callFilter == .all && callLineID == nil
                    ? "line.3.horizontal.decrease"
                    : "line.3.horizontal.decrease.circle.fill"
            )
                .labelStyle(.iconOnly)
        }
        .accessibilityValue(callFilterAccessibilityValue)
    }

    private var callFilterAccessibilityValue: Text {
        let scope: LocalizedStringResource = switch callFilter {
        case .all: "Calls"
        case .missed: "Missed"
        case .unknownCallers: "Unknown Callers"
        }
        guard let selectedCallLine else {
            return Text("\(scope), All SIMs")
        }
        let line = SIMLabelPresentation(line: selectedCallLine, titleStyle: .displayName)
        return Text("\(scope), \(line.accessibilityLabel)")
    }

    private var hasCallHistory: Bool {
        guard case .loaded(let records) = model.callHistory else { return false }
        return !records.isEmpty
    }

    private var hasMessages: Bool {
        guard case .loaded(let conversations) = model.messages else { return false }
        return !conversations.isEmpty
    }

    private var hasOnlineLine: Bool {
        model.devices.contains(where: \.isOnline)
    }

    private var selectedCallLine: DeviceLineIdentity? {
        guard let callLineID else { return nil }
        return model.devices.first(where: { $0.id == callLineID })
    }

    @ViewBuilder
    private func callDetailDestination(for route: CallDetailRoute) -> some View {
        if let selection = callDetailSelection(for: route) {
            if let contact = selection.contact {
                contactDetailDestination(
                    for: contact,
                    requiredLineID: selection.record.lineID
                )
            } else {
                CallDetailScreen(
                    record: selection.record,
                    linePresentation: selection.linePresentation,
                    lines: model.devices,
                    defaultMessageLineID: model.defaultMessageLineID,
                    callAction: {
                        Task {
                            await model.handleExternalDial(
                                number: selection.record.remoteNumber,
                                requiredLineID: selection.record.lineID
                            )
                        }
                    },
                    sendAction: { lineID, number, body in
                        try await model.sendMessage(
                            lineID: lineID,
                            remoteNumber: number,
                            body: body
                        )
                    }
                )
            }
        } else {
            ContentUnavailableView(
                "Call Unavailable",
                systemImage: "phone.down",
                description: Text("This call is no longer in your history.")
            )
        }
    }

    @ViewBuilder
    private func conversationDestination(for conversationID: ConversationID) -> some View {
        if case .loaded(let conversations) = model.messages,
           let conversation = conversations.first(where: { $0.id == conversationID }) {
            let contacts = contactsSnapshot()
            ConversationScreen(
                initialID: conversation.id,
                conversations: conversation.lineAlternatives(in: conversations),
                lines: model.devices,
                contacts: contacts,
                defaultMessageLineID: model.defaultMessageLineID,
                sendAction: { lineID, number, body in
                    try await model.sendMessage(
                        lineID: lineID,
                        remoteNumber: number,
                        body: body
                    )
                },
                sendImageAction: { lineID, number, body, attachment in
                    try await model.sendImageMessage(
                        lineID: lineID,
                        remoteNumber: number,
                        body: body,
                        attachment: attachment
                    )
                },
                retryAction: { messageID in
                    try await model.retryMessage(id: messageID)
                },
                deleteAction: { messageIDs in
                    try model.deleteMessages(ids: messageIDs)
                },
                markReadAction: { selectedConversationID in
                    model.markConversationRead(selectedConversationID)
                },
                callAction: { number in
                    await model.handleExternalDial(number: number)
                }
            )
        } else {
            ContentUnavailableView(
                "Conversation Unavailable",
                systemImage: "message",
                description: Text("This conversation is no longer available.")
            )
        }
    }

    private func contactDetailDestination(
        for contact: ContactRecord,
        requiredLineID: UUID? = nil
    ) -> some View {
        ContactDetailScreen(
            contact: contact,
            lines: model.devices,
            defaultMessageLineID: requiredLineID ?? model.defaultMessageLineID,
            callAction: { number in
                await model.handleExternalDial(
                    number: number,
                    requiredLineID: requiredLineID
                )
            },
            sendAction: { lineID, number, body in
                try await model.sendMessage(
                    lineID: lineID,
                    remoteNumber: number,
                    body: body
                )
            }
        )
    }

    private func callDetailSelection(for route: CallDetailRoute) -> CallDetailSelection? {
        guard case .loaded(let records) = model.callHistory,
              let record = records.first(where: { $0.id == route.recordID }) else {
            return nil
        }
        return CallDetailSelection(
            record: record,
            linePresentation: record.linePresentation(in: model.devices),
            contact: ContactRecordIndex(contacts: contactsSnapshot())
                .contact(for: record.remoteNumber)
        )
    }

    private func contactsSnapshot() -> [ContactRecord] {
        switch model.contactStore.authorizationState {
        case .limited, .authorized:
            (try? model.contactStore.fetchContacts()) ?? []
        case .notDetermined, .denied:
            []
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
