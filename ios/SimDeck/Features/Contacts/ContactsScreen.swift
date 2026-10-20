// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct ContactsScreen: View {
    @Bindable var model: AppModel
    @State private var contactModel: ContactListModel
    @State private var selectedContact: ContactRecord?
    private let startsSynchronization: Bool

    init(
        model: AppModel,
        contactStore: (any ContactStoreProviding)? = nil,
        initialState: ContactListState = .loading,
        startsSynchronization: Bool = true
    ) {
        self.model = model
        _contactModel = State(
            initialValue: ContactListModel(
                store: contactStore ?? model.contactStore,
                state: initialState
            )
        )
        self.startsSynchronization = startsSynchronization
    }

    var body: some View {
        Group {
            switch contactModel.state {
            case .permissionRequired:
                permissionRequiredContent
            case .permissionDenied:
                permissionDeniedContent
            case .loading:
                ProgressView("Loading Contacts…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .loaded(let sections):
                contactsContent(sections)
            case .failed:
                failedContent
            }
        }
        .navigationTitle("Contacts")
        .searchable(
            text: Binding(
                get: { contactModel.searchText },
                set: { contactModel.searchText = $0 }
            ),
            placement: .navigationBarDrawer(displayMode: .always),
            prompt: "Search Contacts"
        )
        .task {
            guard startsSynchronization else { return }
            await contactModel.run()
        }
        .onChange(of: contactModel.state) { _, state in
            if case .loaded = state {
                model.refreshContactNames()
            }
        }
    }

    @ViewBuilder
    private func contactsContent(_ sections: [ContactSection]) -> some View {
        if sections.isEmpty {
            if contactModel.searchText.isEmpty {
                ContentUnavailableView(
                    "No Contacts",
                    systemImage: "person.2",
                    description: Text("Contacts saved in iOS appear here.")
                )
            } else {
                ContentUnavailableView.search(text: contactModel.searchText)
            }
        } else {
            ScrollViewReader { proxy in
                List {
                    ForEach(sections) { section in
                        Section(section.title) {
                            ForEach(section.contacts) { contact in
                                Button {
                                    selectedContact = contact
                                } label: {
                                    ContactRow(contact: contact)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                        .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                .listRowInsets(
                                    EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 44)
                                )
                                .alignmentGuide(.listRowSeparatorLeading) { dimensions in
                                    dimensions[.leading] + 56
                                }
                            }
                        }
                        .id(section.id)
                    }
                }
                .overlay(alignment: .trailing) {
                    if contactModel.searchText.isEmpty {
                        ContactSectionIndex(
                            availableTitles: sections.map(\.title)
                        ) { title in
                            proxy.scrollTo(title, anchor: .top)
                        }
                    }
                }
                .navigationDestination(item: $selectedContact) { contact in
                    ContactDetailScreen(
                        contact: contact,
                        lines: model.devices,
                        defaultMessageLineID: model.defaultMessageLineID,
                        callAction: { number in
                            await model.handleExternalDial(number: number)
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
                .listStyle(.plain)
            }
        }
    }

    private var permissionRequiredContent: some View {
        ContentUnavailableView {
            Label("Allow Contacts Access", systemImage: "person.crop.circle.badge.checkmark")
        } description: {
            Text("Sim Deck reads your contacts to show names and let you call or message them. Contacts stay managed by iOS.")
        } actions: {
            Button("Continue") {
                Task {
                    await contactModel.requestAccess()
                }
            }
            .buttonStyle(.borderedProminent)
        }
    }

    private var permissionDeniedContent: some View {
        ContentUnavailableView {
            Label("Contacts Access Is Off", systemImage: "person.crop.circle.badge.exclamationmark")
        } description: {
            Text("Allow contact access in Settings to browse your iOS contacts in Sim Deck.")
        } actions: {
            Button("Open Settings") {
                guard let url = URL(string: UIApplication.openSettingsURLString) else { return }
                UIApplication.shared.open(url)
            }
            .buttonStyle(.borderedProminent)
        }
    }

    private var failedContent: some View {
        ContentUnavailableView {
            Label("Contacts Unavailable", systemImage: "exclamationmark.triangle")
        } description: {
            Text("Sim Deck could not read your contacts.")
        } actions: {
            Button("Try Again") {
                Task {
                    await contactModel.refresh()
                }
            }
            .buttonStyle(.bordered)
        }
    }
}

private struct ContactSectionIndex: View {
    let availableTitles: [String]
    let onSelect: (String) -> Void

    @State private var activeIndex: Int?

    var body: some View {
        GeometryReader { proxy in
            VStack {
                Spacer(minLength: 0)

                VStack(spacing: 0) {
                    ForEach(Array(ContactSectionIndexResolver.titles.enumerated()), id: \.offset) { index, title in
                        Text(title)
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(.tint)
                            .frame(height: ContactSectionIndexResolver.visibleItemHeight)
                            .opacity(activeIndex == nil || activeIndex == index ? 1 : 0.7)
                    }
                }
                .frame(width: 44, height: ContactSectionIndexResolver.visibleTrackHeight)
                .contentShape(Rectangle())
                .highPriorityGesture(
                    DragGesture(minimumDistance: 0, coordinateSpace: .local)
                        .onChanged { value in
                            selectIndex(
                                for: value.location.y,
                                height: ContactSectionIndexResolver.visibleTrackHeight
                            )
                        }
                        .onEnded { _ in
                            activeIndex = nil
                        }
                )

                Spacer(minLength: 0)
            }
            .frame(width: proxy.size.width, height: proxy.size.height)
        }
        .frame(width: 44)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Contact index")
        .accessibilityHint("Drag vertically to jump between contact sections.")
    }

    private func selectIndex(for locationY: CGFloat, height: CGFloat) {
        let titles = ContactSectionIndexResolver.titles
        guard let index = ContactSectionIndexResolver.index(
            at: locationY,
            height: height,
            itemCount: titles.count
        ) else { return }
        guard index != activeIndex else { return }
        activeIndex = index
        guard let target = ContactSectionIndexResolver.targetTitle(
            for: titles[index],
            availableTitles: availableTitles
        ) else { return }
        UISelectionFeedbackGenerator().selectionChanged()
        onSelect(target)
    }
}

private struct ContactRow: View {
    let contact: ContactRecord

    var body: some View {
        HStack(spacing: 12) {
            ContactAvatar(contact: contact)

            VStack(alignment: .leading, spacing: 2) {
                Text(contact.displayName)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                    .lineLimit(1)

                if !contact.organizationName.isEmpty,
                   contact.organizationName != contact.displayName {
                    Text(contact.organizationName)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .frame(minHeight: 48)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(contact.displayName)
        .accessibilityValue(
            contact.phoneNumbers.isEmpty
                ? Text("No phone number")
                : Text("\(contact.phoneNumbers.count) phone numbers")
        )
    }
}

#if DEBUG
#Preview("Contacts - Loaded") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: PreviewContactFixtures.contacts)
        )
    }
}

#Preview("Contacts - Dark") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: PreviewContactFixtures.contacts)
        )
    }
    .preferredColorScheme(.dark)
}

#Preview("Contacts - Permission") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(authorizationState: .notDetermined),
            initialState: .permissionRequired,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Denied") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(authorizationState: .denied),
            initialState: .permissionDenied,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Loading") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(),
            initialState: .loading,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Empty") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: [])
        )
    }
}

#Preview("Contacts - Error, Chinese, Accessibility") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(),
            initialState: .failed,
            startsSynchronization: false
        )
    }
    .environment(\.locale, Locale(identifier: "zh-Hans"))
    .environment(\.dynamicTypeSize, .accessibility3)
}
#endif
