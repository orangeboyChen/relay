// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

final class ProfileRadioStatusTests: XCTestCase {
    func testNetworkTypesUseFamiliarCellularLabels() {
        XCTAssertEqual(CellularRadioAccessTechnology.gsm.displayName, "2G")
        XCTAssertEqual(CellularRadioAccessTechnology.edge.displayName, "EDGE")
        XCTAssertEqual(CellularRadioAccessTechnology.umts.displayName, "3G")
        XCTAssertEqual(CellularRadioAccessTechnology.hspa.displayName, "H+")
        XCTAssertEqual(CellularRadioAccessTechnology.lte.displayName, "LTE")
        XCTAssertEqual(CellularRadioAccessTechnology.nrNSA.displayName, "5G")
        XCTAssertEqual(CellularRadioAccessTechnology.nrSA.displayName, "5G")
    }

    func testSignalStatusRejectsImpossibleBarCounts() {
        XCTAssertNil(CellularRadioStatus(technology: .lte, signalBars: -1).signalBars)
        XCTAssertNil(CellularRadioStatus(technology: .lte, signalBars: 5).signalBars)
        XCTAssertEqual(CellularRadioStatus(technology: .lte, signalBars: 0).signalBars, 0)
        XCTAssertEqual(CellularRadioStatus(technology: .lte, signalBars: 4).signalBars, 4)
    }

    func testSignalStatusProvidesExactAccessibleBarCount() {
        XCTAssertEqual(
            CellularRadioStatus(technology: .lte, signalBars: 3).signalAccessibilityDescription,
            "3 of 4 signal bars"
        )
        XCTAssertEqual(
            CellularRadioStatus(technology: .unknown, signalBars: nil).signalAccessibilityDescription,
            "Signal unavailable"
        )
    }

    func testSIMAccessibilityStatusDistinguishesLiveAndUnavailableSignals() {
        var line = DeviceLineIdentity(
            id: UUID(),
            numericID: 2,
            deviceName: "Office EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true,
            radioStatus: CellularRadioStatus(technology: .lte, signalBars: 3)
        )

        XCTAssertEqual(line.radioAccessibilityDescription, "LTE, 3 of 4 signal bars")

        line.radioStatus = nil
        XCTAssertEqual(line.radioAccessibilityDescription, "Online, signal unavailable")

        line.isOnline = false
        XCTAssertEqual(line.radioAccessibilityDescription, "Offline")
    }
}
