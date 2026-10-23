// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Intents
import SwiftUI
import SwiftUIIntrospect

struct RootTabView: View {
    @Bindable var model: AppModel

    @State private var navigationPath = NavigationPath()
    @State private var callEditMode: EditMode = .inactive
    @State private var selectedCallEntryIDs: Set<UUID> = []
    @State private var callFilter: CallHistoryFilter = .all
    @State private var callLineID: UUID?
    @State private var messageSearchText = ""
    @State private var messageLineID: UUID?
    @State private var messageEditMode: EditMode = .inactive
    @State private var contactSearchText = ""
    @State private var rootSearchText = ""
    @State private var navigationBarAdapter = RootNavigationBarAdapter()
    @State private var isMessageComposerPresented = false
    @State private var isClearAllCallsConfirmationPresented = false
    @State private var isClearAllCallsErrorPresented = false

    var body: some View {
        rootContent
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
                selectedCallEntryIDs.removeAll()
                messageEditMode = .inactive
            }
            .onChange(of: model.selectedTab, initial: true) { _, selectedTab in
                synchronizeRootSearchText(for: selectedTab)
            }
            .onChange(of: rootSearchText) { _, searchText in
                synchronizeSelectedSearchText(searchText)
            }
            .onChange(of: model.pendingNavigationRequest, initial: true) { _, request in
                guard let request else { return }
                navigate(to: request)
            }
            .confirmationDialog(
                "Clear All Calls?",
                isPresented: $isClearAllCallsConfirmationPresented,
                titleVisibility: .visible
            ) {
                Button("Clear All", role: .destructive) {
                    clearAllCalls()
                }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("All call history on this iPhone will be removed.")
            }
            .alert("Unable to Clear Calls", isPresented: $isClearAllCallsErrorPresented) {
                Button("OK", role: .cancel) {}
            } message: {
                Text("The call history could not be saved.")
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

    private var rootContent: some View {
        ZStack {
            navigationContainer
                .accessibilityHidden(model.callCoordinator.activeCall != nil)

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

    private var navigationContainer: some View {
        NavigationStack(path: $navigationPath) {
            navigationRoot
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

    private var navigationRoot: some View {
        ZStack {
            tabs
        }
        .navigationTitle(rootTitleResource)
        .navigationBarTitleDisplayMode(.inline)
        .introspect(
            .navigationStack,
            on: .iOS(.v26, .v27),
            scope: .ancestor
        ) { navigationController in
            guard navigationPath.isEmpty else { return }
            navigationBarAdapter.configure(
                navigationController: navigationController,
                title: String(localized: rootTitleResource),
                usesLargeTitle: false,
                showsSearch: showsRootSearch,
                searchText: $rootSearchText
            )
        }
        .toolbar(
            model.selectedTab == .keypad ? .hidden : .visible,
            for: .navigationBar
        )
        .toolbar {
            if navigationPath.isEmpty {
                rootToolbar
            }
        }
    }

    private var showsRootSearch: Bool {
        switch model.selectedTab {
        case .messages where !messageEditMode.isEditing, .contacts:
            true
        case .calls, .messages, .profile, .keypad:
            false
        }
    }

    private var rootTitleResource: LocalizedStringResource {
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

    private func synchronizeRootSearchText(for tab: AppTab) {
        switch tab {
        case .messages:
            rootSearchText = messageSearchText
        case .contacts:
            rootSearchText = contactSearchText
        case .calls, .profile, .keypad:
            rootSearchText = ""
        }
    }

    private func synchronizeSelectedSearchText(_ searchText: String) {
        switch model.selectedTab {
        case .messages:
            messageSearchText = searchText
        case .contacts:
            contactSearchText = searchText
        case .calls, .profile, .keypad:
            break
        }
    }

    private func navigate(to request: AppNavigationRequest) {
        callEditMode = .inactive
        selectedCallEntryIDs.removeAll()
        messageEditMode = .inactive
        withAnimation(.snappy) {
            navigationPath = NavigationPath()
            switch request {
            case .conversation(let conversationID):
                model.selectedTab = .messages
                navigationPath.append(conversationID)
            case .call(let recordID):
                model.selectedTab = .calls
                navigationPath.append(CallDetailRoute(recordID: recordID))
            }
        }
        model.consumeNavigationRequest(request)
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
                selectedHistoryEntryIDs: $selectedCallEntryIDs,
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

    @ToolbarContentBuilder
    private var rootToolbar: some ToolbarContent {
        if model.selectedTab == .calls {
            callRootToolbar
        }

        if model.selectedTab == .messages {
            messageRootToolbar
        }
    }

    @ToolbarContentBuilder
    private var callRootToolbar: some ToolbarContent {
        if hasCallHistory {
            if callEditMode.isEditing {
                ToolbarItem(placement: .topBarLeading) {
                    SelectionDoneButton {
                        withAnimation(.snappy) {
                            callEditMode = .inactive
                        }
                    }
                }

                ToolbarItem(placement: .topBarTrailing) {
                    Button("Clear All") {
                        isClearAllCallsConfirmationPresented = true
                    }
                    .buttonBorderShape(.capsule)
                    .disabled(!selectedCallEntryIDs.isEmpty)
                }
            } else {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Edit") {
                        withAnimation {
                            callEditMode = .active
                        }
                    }
                }

                ToolbarItem(placement: .topBarTrailing) {
                    callFilterMenu
                }
            }
        }
    }

    @ToolbarContentBuilder
    private var messageRootToolbar: some ToolbarContent {
        if hasMessages {
            if messageEditMode.isEditing {
                ToolbarItem(placement: .topBarLeading) {
                    SelectionDoneButton {
                        withAnimation(.snappy) {
                            messageEditMode = .inactive
                        }
                    }
                }
            } else {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Edit") {
                        withAnimation(.snappy) {
                            messageEditMode = .active
                        }
                    }
                }
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

    private func clearAllCalls() {
        guard case .loaded(let records) = model.callHistory else { return }
        do {
            try model.deleteCallHistoryRecords(withIDs: Set(records.map(\.id)))
            callEditMode = .inactive
            selectedCallEntryIDs.removeAll()
        } catch {
            isClearAllCallsErrorPresented = true
        }
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
