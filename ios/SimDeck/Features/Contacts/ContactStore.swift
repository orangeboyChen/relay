// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Contacts
import Foundation
import Observation

@MainActor
protocol ContactStoreProviding: AnyObject {
    var authorizationState: ContactAuthorizationState { get }
    func requestAccess() async throws -> Bool
    func fetchContacts() throws -> [ContactRecord]
    func changes() -> AsyncStream<Void>
}

@MainActor
final class SystemContactStore: ContactStoreProviding {
    private final class ObserverToken: @unchecked Sendable {
        var value: (any NSObjectProtocol)?
    }

    private let store: CNContactStore
    private let notificationCenter: NotificationCenter

    init(
        store: CNContactStore = CNContactStore(),
        notificationCenter: NotificationCenter = .default
    ) {
        self.store = store
        self.notificationCenter = notificationCenter
    }

    var authorizationState: ContactAuthorizationState {
        switch CNContactStore.authorizationStatus(for: .contacts) {
        case .notDetermined:
            .notDetermined
        case .restricted, .denied:
            .denied
        case .limited:
            .limited
        case .authorized:
            .authorized
        @unknown default:
            .denied
        }
    }

    func requestAccess() async throws -> Bool {
        try await store.requestAccess(for: .contacts)
    }

    func fetchContacts() throws -> [ContactRecord] {
        let keys: [CNKeyDescriptor] = [
            CNContactIdentifierKey as CNKeyDescriptor,
            CNContactGivenNameKey as CNKeyDescriptor,
            CNContactFamilyNameKey as CNKeyDescriptor,
            CNContactOrganizationNameKey as CNKeyDescriptor,
            CNContactPhoneNumbersKey as CNKeyDescriptor,
            CNContactThumbnailImageDataKey as CNKeyDescriptor,
        ]
        let request = CNContactFetchRequest(keysToFetch: keys)
        request.sortOrder = .userDefault

        var contacts: [ContactRecord] = []
        try store.enumerateContacts(with: request) { contact, _ in
            let phoneNumbers = contact.phoneNumbers.enumerated().map { index, phoneNumber in
                ContactPhoneNumber(
                    id: "\(contact.identifier):\(index)",
                    label: CNLabeledValue<NSString>.localizedString(
                        forLabel: phoneNumber.label ?? CNLabelPhoneNumberMain
                    ),
                    value: phoneNumber.value.stringValue
                )
            }
            contacts.append(
                ContactRecord(
                    id: contact.identifier,
                    givenName: contact.givenName,
                    familyName: contact.familyName,
                    organizationName: contact.organizationName,
                    phoneNumbers: phoneNumbers,
                    thumbnailImageData: contact.thumbnailImageData
                )
            )
        }
        return contacts
    }

    func changes() -> AsyncStream<Void> {
        AsyncStream { continuation in
            let observerToken = ObserverToken()
            observerToken.value = notificationCenter.addObserver(
                forName: .CNContactStoreDidChange,
                object: nil,
                queue: .main
            ) { _ in
                continuation.yield()
            }
            continuation.onTermination = { [notificationCenter, observerToken] _ in
                if let observer = observerToken.value {
                    notificationCenter.removeObserver(observer)
                }
            }
        }
    }
}

@MainActor
@Observable
final class ContactListModel {
    private(set) var state: ContactListState
    var searchText: String {
        didSet {
            guard searchText != oldValue else { return }
            rebuildSections()
        }
    }

    @ObservationIgnored private let store: any ContactStoreProviding
    @ObservationIgnored private var contacts: [ContactRecord]

    init(
        store: any ContactStoreProviding,
        state: ContactListState = .loading,
        searchText: String = ""
    ) {
        self.store = store
        self.state = state
        self.searchText = searchText
        self.contacts = []
    }

    func run() async {
        await refresh()
        for await _ in store.changes() {
            guard !Task.isCancelled else { return }
            await refresh()
        }
    }

    func requestAccess() async {
        state = .loading
        do {
            _ = try await store.requestAccess()
            await refresh()
        } catch {
            state = .permissionDenied
        }
    }

    func refresh() async {
        switch store.authorizationState {
        case .notDetermined:
            state = .permissionRequired
        case .denied:
            state = .permissionDenied
        case .limited, .authorized:
            do {
                contacts = try store.fetchContacts()
                rebuildSections()
            } catch {
                state = .failed
            }
        }
    }

    private func rebuildSections() {
        state = .loaded(ContactDirectory.sections(for: contacts, matching: searchText))
    }
}
