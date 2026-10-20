// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

final class DefaultLineRoutingTests: XCTestCase {
    func testPreferredOnlineLineIsSelected() {
        let first = makeLine(numericID: 1, isOnline: true)
        let preferred = makeLine(numericID: 2, isOnline: true)

        let resolution = OutgoingCallLineRouter.resolve(
            lines: [first, preferred],
            preferredLineID: preferred.id
        )

        XCTAssertEqual(resolution, .ready(preferred))
    }

    func testOnlyOnlineLineIsSelectedWhenPreferenceIsUnavailable() {
        let offlinePreferred = makeLine(numericID: 1, isOnline: false)
        let online = makeLine(numericID: 2, isOnline: true)

        let resolution = OutgoingCallLineRouter.resolve(
            lines: [offlinePreferred, online],
            preferredLineID: offlinePreferred.id
        )

        XCTAssertEqual(resolution, .ready(online))
    }

    func testMultipleOnlineLinesRequireSelectionWithoutAvailablePreference() {
        let first = makeLine(numericID: 1, isOnline: true)
        let second = makeLine(numericID: 2, isOnline: true)

        let resolution = OutgoingCallLineRouter.resolve(
            lines: [first, second],
            preferredLineID: UUID()
        )

        XCTAssertEqual(resolution, .selectionRequired([first, second]))
    }

    func testNoOnlineLinesAreUnavailable() {
        let line = makeLine(numericID: 1, isOnline: false)

        XCTAssertEqual(
            OutgoingCallLineRouter.resolve(lines: [line], preferredLineID: line.id),
            .unavailable
        )
    }

    func testRequiredOnlineLineIsSelectedEvenWhenAnotherLineIsPreferred() {
        let required = makeLine(numericID: 1, isOnline: true)
        let other = makeLine(numericID: 2, isOnline: true)

        XCTAssertEqual(
            OutgoingCallLineRouter.resolve(lines: [other, required], requiredLineID: required.id),
            .ready(required)
        )
    }

    func testOfflineRequiredLineNeedsExplicitSelectionInsteadOfSilentFallback() {
        let required = makeLine(numericID: 1, isOnline: false)
        let other = makeLine(numericID: 2, isOnline: true)

        XCTAssertEqual(
            OutgoingCallLineRouter.resolve(lines: [required, other], requiredLineID: required.id),
            .selectionRequired([other])
        )
    }

    func testUserDefaultsStorePersistsAndClearsLineIDs() throws {
        let suiteName = "DefaultLineRoutingTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        let store = UserDefaultsDefaultLineStore(defaults: defaults)
        let callLineID = UUID()
        let messageLineID = UUID()

        store.callLineID = callLineID
        store.messageLineID = messageLineID

        let reloadedStore = UserDefaultsDefaultLineStore(defaults: defaults)
        XCTAssertEqual(reloadedStore.callLineID, callLineID)
        XCTAssertEqual(reloadedStore.messageLineID, messageLineID)

        reloadedStore.callLineID = nil
        reloadedStore.messageLineID = nil
        XCTAssertNil(store.callLineID)
        XCTAssertNil(store.messageLineID)
    }

    @MainActor
    func testAppModelPersistsChangedDefaultLines() {
        let store = DefaultLineMemoryStore()
        let model = AppModel(defaultLineStore: store)
        let callLineID = UUID()
        let messageLineID = UUID()

        model.defaultCallLineID = callLineID
        model.defaultMessageLineID = messageLineID

        XCTAssertEqual(store.callLineID, callLineID)
        XCTAssertEqual(store.messageLineID, messageLineID)
    }

    private func makeLine(numericID: Int, isOnline: Bool) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: UUID(),
            numericID: numericID,
            deviceName: "Device \(numericID)",
            lineName: "Line \(numericID)",
            phoneNumber: "13500000000",
            isOnline: isOnline
        )
    }
}

private final class DefaultLineMemoryStore: DefaultLineStoring {
    var callLineID: UUID?
    var messageLineID: UUID?
}
