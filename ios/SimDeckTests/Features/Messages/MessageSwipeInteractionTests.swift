// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class MessageSwipeInteractionTests: XCTestCase {
    func testLeftDragTracksWithinDirectionalLimit() {
        XCTAssertEqual(
            MessageSwipeInteraction.offset(horizontal: 40, vertical: 4),
            0
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

    func testGestureBeginsOnlyForLeftwardHorizontalVelocity() {
        XCTAssertTrue(
            MessageSwipeInteraction.shouldBegin(horizontalVelocity: -80, verticalVelocity: 20)
        )
        XCTAssertFalse(
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
