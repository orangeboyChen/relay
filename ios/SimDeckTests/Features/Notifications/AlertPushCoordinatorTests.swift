// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import UserNotifications
import XCTest
@testable import SimDeck

@MainActor
final class AlertPushCoordinatorTests: XCTestCase {
    func testDefaultNotificationActionPrefersTargetConversationIdentifier() {
        let content = UNMutableNotificationContent()
        content.threadIdentifier = "thread"
        content.targetContentIdentifier = "target"

        XCTAssertEqual(
            AlertNotificationMetadata.conversationIdentifier(
                content: content,
                actionIdentifier: UNNotificationDefaultActionIdentifier
            ),
            "target"
        )
    }

    func testNotificationActionsOtherThanOpeningDoNotNavigate() {
        let content = UNMutableNotificationContent()
        content.threadIdentifier = "thread"

        XCTAssertNil(
            AlertNotificationMetadata.conversationIdentifier(
                content: content,
                actionIdentifier: AlertNotificationMetadata.copyCodeActionIdentifier
            )
        )
    }

    func testEnablingRequestsRegistrationAndUploadsReceivedToken() async throws {
        let registrar = AlertPushTokenRegistrar()
        let installationID = UUID()
        let token = Data(repeating: 7, count: 32)
        var registrationRequests = 0
        let coordinator = AlertPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            requestRemoteNotificationRegistration: { registrationRequests += 1 }
        )
        coordinator.configure(installationID: installationID)

        try await coordinator.setEnabled(true)
        try await coordinator.receiveToken(token)

        XCTAssertEqual(registrationRequests, 1)
        XCTAssertEqual(
            registrar.registrations,
            [.init(token: token, kind: .alert, environment: .sandbox, installationID: installationID)]
        )
    }

    func testDisablingDeletesAlertTokenForCurrentInstallation() async throws {
        let registrar = AlertPushTokenRegistrar()
        let installationID = UUID()
        let coordinator = AlertPushCoordinator(
            tokenRegistrar: registrar,
            environment: .production,
            requestRemoteNotificationRegistration: {}
        )
        coordinator.configure(installationID: installationID)
        try await coordinator.setEnabled(true)
        try await coordinator.receiveToken(Data(repeating: 3, count: 32))

        try await coordinator.setEnabled(false)

        XCTAssertEqual(
            registrar.deletions,
            [.init(kind: .alert, environment: .production, installationID: installationID)]
        )
    }

    func testDisabledCoordinatorIgnoresTokensDeliveredByTheApplicationDelegate() async throws {
        let registrar = AlertPushTokenRegistrar()
        let coordinator = AlertPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            requestRemoteNotificationRegistration: {}
        )
        coordinator.configure(installationID: UUID())
        try await coordinator.setEnabled(false)

        try await coordinator.receiveToken(Data(repeating: 9, count: 32))

        XCTAssertTrue(registrar.registrations.isEmpty)
        XCTAssertEqual(registrar.deletions.count, 1)
    }

    func testFailedDeletionIsRetriedBeforeReplacementTokenRegistration() async throws {
        let registrar = AlertPushTokenRegistrar(error: AlertPushTestError.failed)
        let coordinator = AlertPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            requestRemoteNotificationRegistration: {}
        )
        coordinator.configure(installationID: UUID())

        do {
            try await coordinator.setEnabled(false)
            XCTFail("Expected alert token deletion to fail")
        } catch {
            XCTAssertEqual(error as? AlertPushTestError, .failed)
        }
        registrar.error = nil
        try await coordinator.setEnabled(true)
        try await coordinator.receiveToken(Data(repeating: 5, count: 32))

        XCTAssertEqual(registrar.deletions.count, 2)
        XCTAssertEqual(registrar.registrations.count, 1)
    }

    func testStoppingDeletesTokenAndAllowsRestart() async throws {
        let registrar = AlertPushTokenRegistrar()
        let installationID = UUID()
        var registrationRequests = 0
        let coordinator = AlertPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            requestRemoteNotificationRegistration: { registrationRequests += 1 }
        )
        coordinator.configure(installationID: installationID)
        try await coordinator.setEnabled(true)

        try await coordinator.stop()
        coordinator.start(installationID: installationID)
        try await Task.sleep(for: .milliseconds(50))

        XCTAssertEqual(registrar.deletions.count, 1)
        XCTAssertEqual(registrationRequests, 2)
    }
}

@MainActor
private final class AlertPushTokenRegistrar: PushTokenRegistering {
    struct Registration: Equatable {
        let token: Data
        let kind: PushTokenKind
        let environment: PushEnvironment
        let installationID: UUID
    }

    struct Deletion: Equatable {
        let kind: PushTokenKind
        let environment: PushEnvironment
        let installationID: UUID
    }

    var error: Error?
    private(set) var registrations: [Registration] = []
    private(set) var deletions: [Deletion] = []

    init(error: Error? = nil) {
        self.error = error
    }

    func registerPushToken(
        _ token: Data,
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws {
        registrations.append(.init(
            token: token,
            kind: kind,
            environment: environment,
            installationID: installationID
        ))
        if let error { throw error }
    }

    func unregisterPushToken(
        kind: PushTokenKind,
        environment: PushEnvironment,
        installationID: UUID
    ) async throws {
        deletions.append(.init(
            kind: kind,
            environment: environment,
            installationID: installationID
        ))
        if let error { throw error }
    }
}

private enum AlertPushTestError: Error, Equatable {
    case failed
}
