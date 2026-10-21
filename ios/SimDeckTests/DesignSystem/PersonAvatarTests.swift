// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Testing
@testable import SimDeck

struct PersonAvatarTests {
    @Test func usesOrganizationPresentationWhenOrganizationIsAvailable() {
        #expect(
            PersonAvatarFallback.resolve(
                displayName: "Apple Support",
                organizationName: "Apple"
            ) == .organization
        )
    }

    @Test func buildsWesternMonogramFromFirstTwoNameParts() {
        #expect(
            PersonAvatarFallback.resolve(
                displayName: "Ada Lovelace",
                organizationName: nil
            ) == .monogram("AL")
        )
    }

    @Test func buildsSingleCharacterMonogramForUnspacedChineseName() {
        #expect(
            PersonAvatarFallback.resolve(
                displayName: "张三",
                organizationName: nil
            ) == .monogram("张")
        )
    }

    @Test func usesPlaceholderWhenIdentityIsEmpty() {
        #expect(
            PersonAvatarFallback.resolve(
                displayName: "  ",
                organizationName: "\n"
            ) == .placeholder
        )
    }
}
