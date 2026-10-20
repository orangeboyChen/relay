#if DEBUG
import Foundation

enum PreviewContactFixtures {
    static let contacts = [
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
