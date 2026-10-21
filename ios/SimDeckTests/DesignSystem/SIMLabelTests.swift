// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Testing
@testable import SimDeck

struct SIMLabelTests {
    @Test func displayNameStyleUsesTheResolvedLineName() {
        let presentation = SIMLabelPresentation(
            number: 2,
            displayName: "135 0000 0000",
            phoneNumber: "13500000000",
            lineName: "Work",
            titleStyle: .displayName
        )

        #expect(presentation.title == "135 0000 0000")
        #expect(presentation.accessibilityLabel.contains("13500000000"))
    }

    @Test func identifierStyleAlwaysUsesTheSIMNumber() {
        let presentation = SIMLabelPresentation(
            number: 7,
            displayName: "Work",
            phoneNumber: "",
            lineName: "Work",
            titleStyle: .identifier
        )

        #expect(presentation.title == String(localized: "SIM 7"))
        #expect(presentation.accessibilityLabel.contains("Work"))
    }

    @Test func displayNameFallsBackToLineNameThenIdentifier() {
        let lineNamePresentation = SIMLabelPresentation(
            number: 3,
            displayName: " ",
            phoneNumber: "",
            lineName: "Travel",
            titleStyle: .displayName
        )
        let identifierPresentation = SIMLabelPresentation(
            number: 4,
            displayName: nil,
            phoneNumber: nil,
            lineName: nil,
            titleStyle: .displayName
        )

        #expect(lineNamePresentation.title == "Travel")
        #expect(identifierPresentation.title == String(localized: "SIM 4"))
    }

    @Test func unavailableSnapshotHasAStableFallback() {
        let presentation = SIMLabelPresentation(
            snapshot: nil,
            titleStyle: .displayName
        )

        #expect(presentation.number == nil)
        #expect(presentation.title == String(localized: "Unavailable SIM"))
        #expect(presentation.accessibilityLabel == presentation.title)
    }
}
