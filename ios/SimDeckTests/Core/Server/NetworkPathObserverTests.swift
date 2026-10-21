// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class NetworkPathObserverTests: XCTestCase {
    func testSatisfiedInterfaceChangeRequiresConnectionMigration() {
        var tracker = NetworkPathStateTracker()

        XCTAssertNil(tracker.update(isAvailable: true, interface: .wifi))
        XCTAssertEqual(
            tracker.update(isAvailable: true, interface: .cellular),
            NetworkPathUpdate(
                isAvailable: true,
                interface: .cellular,
                requiresConnectionMigration: true
            )
        )
    }

    func testRestoredNetworkRequiresConnectionMigration() {
        var tracker = NetworkPathStateTracker()

        XCTAssertNil(tracker.update(isAvailable: true, interface: .wifi))
        XCTAssertEqual(
            tracker.update(isAvailable: false, interface: .unavailable),
            NetworkPathUpdate(
                isAvailable: false,
                interface: .unavailable,
                requiresConnectionMigration: false
            )
        )
        XCTAssertEqual(
            tracker.update(isAvailable: true, interface: .wifi),
            NetworkPathUpdate(
                isAvailable: true,
                interface: .wifi,
                requiresConnectionMigration: true
            )
        )
    }

    func testRepeatedAvailablePathDoesNotRequireConnectionMigration() {
        var tracker = NetworkPathStateTracker()

        XCTAssertNil(tracker.update(isAvailable: true, interface: .wifi))
        XCTAssertEqual(
            tracker.update(isAvailable: true, interface: .wifi),
            NetworkPathUpdate(
                isAvailable: true,
                interface: .wifi,
                requiresConnectionMigration: false
            )
        )
    }
}
