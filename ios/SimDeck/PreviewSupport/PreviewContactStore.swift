// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

#if DEBUG
import Foundation

enum PreviewContactFixtures {
    private static let featuredContacts = [
        ContactRecord(
            id: "apple",
            givenName: "Apple",
            familyName: "(中国大陆)",
            organizationName: "Apple (中国大陆)",
            phoneNumbers: [
                ContactPhoneNumber(id: "apple-care", label: "AppleCare", value: "+86 400 627 2273"),
                ContactPhoneNumber(id: "apple-store", label: "Apple Store", value: "+86 400 666 8800"),
            ],
            thumbnailImageData: nil
        ),
        ContactRecord(
            id: "brian",
            givenName: "Brian",
            familyName: "Man",
            organizationName: "",
            phoneNumbers: [
                ContactPhoneNumber(id: "brian-mobile", label: "mobile", value: "13500000000"),
            ],
            thumbnailImageData: nil
        ),
        ContactRecord(
            id: "service",
            givenName: "Service",
            familyName: "Desk",
            organizationName: "Sim Deck Support",
            phoneNumbers: [
                ContactPhoneNumber(id: "service-work", label: "work", value: "13800000000"),
            ],
            thumbnailImageData: nil
        ),
    ]

    private static let indexedNames = [
        "Alpha", "Charlie", "Delta", "Echo", "Foxtrot", "Gamma", "Hotel", "India",
        "Juliet", "Kilo", "Lima", "Mike", "November", "Oscar", "Papa", "Quebec",
        "Romeo", "Tango", "Uniform", "Victor", "Whiskey", "Xray", "Yankee", "Zulu",
        "13500000000",
    ]

    static let contacts = featuredContacts + indexedNames.enumerated().map { index, name in
        ContactRecord(
            id: "indexed-\(index)",
            givenName: name,
            familyName: "",
            organizationName: "",
            phoneNumbers: [
                ContactPhoneNumber(
                    id: "indexed-\(index)-mobile",
                    label: "mobile",
                    value: "13500000000"
                ),
            ],
            thumbnailImageData: nil
        )
    }
}

@MainActor
final class PreviewContactStore: ContactStoreProviding {
    var authorizationState: ContactAuthorizationState
    private let contacts: [ContactRecord]

    init(
        authorizationState: ContactAuthorizationState = .authorized,
        contacts: [ContactRecord] = []
    ) {
        self.authorizationState = authorizationState
        self.contacts = contacts
    }

    func requestAccess() async throws -> Bool {
        authorizationState = .authorized
        return true
    }

    func fetchContacts() throws -> [ContactRecord] {
        contacts
    }

    func changes() -> AsyncStream<Void> {
        AsyncStream { continuation in
            continuation.finish()
        }
    }
}
#endif
