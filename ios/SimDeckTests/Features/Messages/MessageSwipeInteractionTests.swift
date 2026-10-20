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

    func testGestureBeginsOnlyForHorizontalVelocity() {
        XCTAssertTrue(
            MessageSwipeInteraction.shouldBegin(horizontalVelocity: 80, verticalVelocity: 20)
        )
        XCTAssertFalse(
            MessageSwipeInteraction.shouldBegin(horizontalVelocity: 20, verticalVelocity: 80)
        )
        XCTAssertFalse(
            MessageSwipeInteraction.shouldBegin(horizontalVelocity: 40, verticalVelocity: 40)
        )
    }

    func testTimestampRevealIsVisibleOnlyDuringLeftDrag() {
        var state = MessageTimestampRevealState()

        state.update(horizontal: -72, vertical: 4)
        XCTAssertEqual(state.offset, -72)
        XCTAssertEqual(state.progress, 1)

        state.end()
        XCTAssertEqual(state.offset, 0)
        XCTAssertEqual(state.progress, 0)
    }

    func testRightOrVerticalDragDoesNotRevealTimestamps() {
        var state = MessageTimestampRevealState()

        state.update(horizontal: 72, vertical: 4)
        XCTAssertEqual(state.offset, 0)
        XCTAssertEqual(state.progress, 0)

        state.update(horizontal: -20, vertical: 40)
        XCTAssertEqual(state.offset, 0)
        XCTAssertEqual(state.progress, 0)
    }
}
