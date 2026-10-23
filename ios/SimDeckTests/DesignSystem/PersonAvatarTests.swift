// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Testing
import UIKit
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

    @Test func usesPlaceholderForNumericCallerIdentity() {
        #expect(
            PersonAvatarFallback.resolve(
                displayName: "10086",
                organizationName: nil
            ) == .placeholder
        )
    }

    @Test func notificationFallbackRendererAlwaysProducesAnImage() {
        #expect(PersonAvatarImageRenderer.pngData(displayName: "Alice Brown") != nil)
        #expect(PersonAvatarImageRenderer.pngData(displayName: "10086") != nil)
    }

    @Test func appAndNotificationFallbacksShareOneRenderer() throws {
        let image = try #require(
            PersonAvatarImageRenderer.image(
                displayName: "Alice Brown",
                size: 44,
                scale: 3
            )
        )
        let notificationData = try #require(
            PersonAvatarImageRenderer.pngData(
                displayName: "Alice Brown",
                size: 44
            )
        )
        let notificationImage = try #require(UIImage(data: notificationData))

        #expect(image.size == CGSize(width: 44, height: 44))
        #expect(image.scale == 3)
        #expect(notificationImage.size == CGSize(width: 44, height: 44))
    }
}
