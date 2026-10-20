import SwiftUI
import UIKit

struct ContactsScreen: View {
    @Bindable var model: AppModel
    @State private var contactModel: ContactListModel
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
                store: contactStore ?? SystemContactStore(),
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
            List {
                ForEach(sections) { section in
                    Section(section.title) {
                        ForEach(section.contacts) { contact in
                            NavigationLink {
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
                            } label: {
                                ContactRow(contact: contact)
                            }
                        }
                    }
                }
            }
            .listStyle(.plain)
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
