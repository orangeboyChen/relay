import XCTest
@testable import SimDeck

final class MessageDeliveryStatusPresentationTests: XCTestCase {
    func testDeliveryStatusIsShownOnlyForSentMessages() {
        let received = MessageItem(
            id: UUID(),
            direction: .received,
            body: "Incoming",
            attachment: nil,
            sentAt: .now,
            status: .delivered
        )
        let sent = MessageItem(
            id: UUID(),
            direction: .sent,
            body: "Outgoing",
            attachment: nil,
            sentAt: .now,
            status: .delivered
        )

        XCTAssertFalse(received.displaysDeliveryStatus)
        XCTAssertTrue(sent.displaysDeliveryStatus)
    }
}
