import Intents
import UserNotifications
import XCTest
@testable import SimDeck

final class CommunicationNotificationTests: XCTestCase {
    func testBuildsIncomingMessageIntentWithResolvedContactAndAvatar() throws {
        let avatar = Data([0x89, 0x50, 0x4e, 0x47])
        let builder = CommunicationNotificationContentBuilder(
            contacts: ContactResolverStub(
                identity: CommunicationNotificationIdentity(
                    displayName: "Alice",
                    contactIdentifier: "contact-1",
                    avatarData: avatar
                )
            )
        )

        let intent = builder.makeIntent(
            remoteNumber: "+8613500000000",
            messageBody: "New message",
            conversationIdentifier: "device-1:+8613500000000"
        )

        XCTAssertEqual(intent.sender?.displayName, "Alice")
        XCTAssertEqual(intent.sender?.personHandle?.value, "+8613500000000")
        XCTAssertEqual(intent.sender?.contactIdentifier, "contact-1")
        XCTAssertNotNil(intent.sender?.image)
        XCTAssertEqual(intent.content, "New message")
        XCTAssertEqual(intent.conversationIdentifier, "device-1:+8613500000000")
    }

    func testFallsBackToNumberAndAlwaysKeepsNumberInSubtitle() {
        let builder = CommunicationNotificationContentBuilder(contacts: ContactResolverStub(identity: nil))
        let original = UNMutableNotificationContent()
        original.title = "Sim Deck"
        original.body = "New message"

        let intent = builder.makeIntent(
            remoteNumber: "13500000000",
            messageBody: "New message",
            conversationIdentifier: "device-2:13500000000"
        )
        let result = builder.update(
            original,
            using: intent,
            remoteNumber: "13500000000",
            numericID: 2,
            conversationIdentifier: "device-2:13500000000"
        )

        XCTAssertEqual(intent.sender?.displayName, "13500000000")
        XCTAssertNil(intent.sender?.contactIdentifier)
        XCTAssertNil(intent.sender?.image)
        XCTAssertEqual(result.subtitle, "13500000000 · SIM 2")
        XCTAssertEqual(result.threadIdentifier, "device-2:13500000000")
        XCTAssertEqual(result.targetContentIdentifier, "device-2:13500000000")
    }
}

private struct ContactResolverStub: CommunicationNotificationContactResolving {
    let identity: CommunicationNotificationIdentity?

    func identity(for phoneNumber: String) -> CommunicationNotificationIdentity? {
        identity
    }
}
