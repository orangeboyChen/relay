// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

@MainActor
final class ContactListModelTests: XCTestCase {
    func testPhoneNumberPreservesNativeContactFormatting() {
        let number = ContactPhoneNumber(
            id: "formatted",
            label: "mobile",
            value: "+86 (400) 627 2273"
        )

        XCTAssertEqual(number.displayValue, "+86 (400) 627 2273")
    }

    func testDirectorySortsSectionsAndKeepsNumberedContactsLast() {
        let contacts = [
            contact(id: "3", givenName: "13500000000"),
            contact(id: "2", givenName: "Beta"),
            contact(id: "1", givenName: "Alpha"),
        ]

        let sections = ContactDirectory.sections(for: contacts)

        XCTAssertEqual(sections.map(\.title), ["A", "B", "#"])
        XCTAssertEqual(sections.flatMap(\.contacts).map(\.id), ["1", "2", "3"])
    }

    func testChineseNameUsesFamilyNameFirstWithoutWhitespaceAndSortsByPinyin() {
        let contacts = [
            contact(id: "li", givenName: "四", familyName: "李"),
            contact(id: "zhang", givenName: "三", familyName: "张"),
            contact(id: "alpha", givenName: "Alpha"),
        ]

        let sections = ContactDirectory.sections(for: contacts)

        XCTAssertEqual(contacts[0].displayName, "李四")
        XCTAssertEqual(contacts[1].displayName, "张三")
        XCTAssertEqual(sections.map(\.title), ["A", "L", "Z"])
        XCTAssertEqual(sections.flatMap(\.contacts).map(\.id), ["alpha", "li", "zhang"])
    }

    func testContactNameIndexMatchesChineseCountryCodeAndNativeFormatting() {
        let index = ContactNameIndex(
            contacts: [
                contact(
                    id: "zhang",
                    givenName: "三",
                    familyName: "张",
                    phoneNumber: "+86 135 0000 0000"
                ),
            ]
        )

        XCTAssertEqual(index.name(for: "13500000000"), "张三")
        XCTAssertEqual(index.name(for: "0086 135 0000 0000"), "张三")
    }

    func testSectionIndexSelectsExactOrNearestAvailableSection() {
        let availableTitles = ["A", "L", "Z", "#"]

        XCTAssertEqual(
            ContactSectionIndexResolver.targetTitle(
                for: "L",
                availableTitles: availableTitles
            ),
            "L"
        )
        XCTAssertEqual(
            ContactSectionIndexResolver.targetTitle(
                for: "M",
                availableTitles: availableTitles
            ),
            "Z"
        )
        XCTAssertEqual(
            ContactSectionIndexResolver.targetTitle(
                for: "#",
                availableTitles: availableTitles
            ),
            "#"
        )
    }

    func testDirectorySearchesNamesOrganizationsAndPhoneNumbers() {
        let contacts = [
            contact(
                id: "apple",
                givenName: "Apple",
                organizationName: "Apple China",
                phoneNumber: "+86 400 627 2273"
            ),
            contact(id: "beta", givenName: "Beta", phoneNumber: "13800000000"),
        ]

        XCTAssertEqual(ContactDirectory.sections(for: contacts, matching: "China").first?.contacts.map(\.id), ["apple"])
        XCTAssertEqual(ContactDirectory.sections(for: contacts, matching: "400627").first?.contacts.map(\.id), ["apple"])
        XCTAssertEqual(ContactDirectory.sections(for: contacts, matching: "beta").first?.contacts.map(\.id), ["beta"])
    }

    func testModelDoesNotFetchBeforePermissionIsGranted() async {
        let store = ContactStoreStub(authorizationState: .notDetermined, contacts: [contact(id: "1", givenName: "Alpha")])
        let model = ContactListModel(store: store)

        await model.refresh()

        XCTAssertEqual(model.state, .permissionRequired)
        XCTAssertEqual(store.fetchCount, 0)
    }

    func testModelLoadsContactsAfterAccessIsGranted() async {
        let store = ContactStoreStub(authorizationState: .notDetermined, contacts: [contact(id: "1", givenName: "Alpha")])
        store.grantsAccess = true
        let model = ContactListModel(store: store)

        await model.requestAccess()

        XCTAssertEqual(model.state, .loaded([
            ContactSection(id: "A", title: "A", contacts: store.contacts),
        ]))
        XCTAssertEqual(store.fetchCount, 1)
    }

    func testModelRebuildsLoadedSectionsWhenSearchChanges() async {
        let store = ContactStoreStub(
            authorizationState: .authorized,
            contacts: [
                contact(id: "1", givenName: "Alpha"),
                contact(id: "2", givenName: "Beta"),
            ]
        )
        let model = ContactListModel(store: store)
        await model.refresh()

        model.searchText = "Beta"

        guard case .loaded(let sections) = model.state else {
            return XCTFail("Expected loaded contacts")
        }
        XCTAssertEqual(sections.flatMap(\.contacts).map(\.id), ["2"])
        XCTAssertEqual(store.fetchCount, 1)
    }

    func testRunReloadsContactsAfterTheSystemDirectoryChanges() async {
        let store = ContactStoreStub(
            authorizationState: .authorized,
            contacts: [contact(id: "1", givenName: "Alpha")]
        )
        let model = ContactListModel(store: store)
        let runTask = Task {
            await model.run()
        }

        while store.fetchCount < 1 || !store.isObservingChanges {
            await Task.yield()
        }
        store.contacts = [contact(id: "2", givenName: "Beta")]
        store.emitChange()
        while store.fetchCount < 2 {
            await Task.yield()
        }

        guard case .loaded(let sections) = model.state else {
            runTask.cancel()
            return XCTFail("Expected reloaded contacts")
        }
        XCTAssertEqual(sections.flatMap(\.contacts).map(\.id), ["2"])
        runTask.cancel()
    }

    private func contact(
        id: String,
        givenName: String,
        familyName: String = "",
        organizationName: String = "",
        phoneNumber: String = ""
    ) -> ContactRecord {
        ContactRecord(
            id: id,
            givenName: givenName,
            familyName: familyName,
            organizationName: organizationName,
            phoneNumbers: phoneNumber.isEmpty
                ? []
                : [ContactPhoneNumber(id: "\(id):0", label: "mobile", value: phoneNumber)],
            thumbnailImageData: nil
        )
    }
}

@MainActor
private final class ContactStoreStub: ContactStoreProviding {
    var authorizationState: ContactAuthorizationState
    var contacts: [ContactRecord]
    var grantsAccess = false
    private(set) var fetchCount = 0
    private var changeContinuation: AsyncStream<Void>.Continuation?

    var isObservingChanges: Bool {
        changeContinuation != nil
    }

    init(authorizationState: ContactAuthorizationState, contacts: [ContactRecord]) {
        self.authorizationState = authorizationState
        self.contacts = contacts
    }

    func requestAccess() async throws -> Bool {
        if grantsAccess {
            authorizationState = .authorized
        }
        return grantsAccess
    }

    func fetchContacts() throws -> [ContactRecord] {
        fetchCount += 1
        return contacts
    }

    func changes() -> AsyncStream<Void> {
        AsyncStream { continuation in
            changeContinuation = continuation
        }
    }

    func emitChange() {
        changeContinuation?.yield()
    }
}
