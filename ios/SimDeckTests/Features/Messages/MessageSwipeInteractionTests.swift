import XCTest
@testable import SimDeck

final class MessageSwipeInteractionTests: XCTestCase {
    func testHorizontalDragTracksWithinDirectionalLimits() {
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: 40, vertical: 4),
            40
        )
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: 400, vertical: 4),
            MessageSwipeInteraction.maximumReplyOffset
        )
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: -400, vertical: 4),
            -MessageSwipeInteraction.maximumTimestampOffset
        )
    }

    func testVerticalDragDoesNotMoveMessageBubble() {
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: 20, vertical: 40),
            0
        )
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: -20, vertical: -40),
            0
        )
    }

    func testReplyRequiresDominantRightwardDragPastThreshold() {
        XCTAssertFalse(MessageSwipeInteraction.shouldReply(horizontal: 51, vertical: 0))
        XCTAssertTrue(MessageSwipeInteraction.shouldReply(horizontal: 52, vertical: 0))
        XCTAssertFalse(MessageSwipeInteraction.shouldReply(horizontal: -80, vertical: 0))
        XCTAssertFalse(MessageSwipeInteraction.shouldReply(horizontal: 80, vertical: 90))
    }
}
