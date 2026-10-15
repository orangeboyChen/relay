import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class VoIPPushCoordinatorTests: XCTestCase {
    func testUploadsVoIPTokenWithCurrentInstallationIdentity() async throws {
        let registrar = RecordingPushTokenRegistrar()
        let installationID = UUID()
        let token = Data((0..<32).map(UInt8.init))
        let coordinator = VoIPPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            incomingPushHandler: { _ in }
        )
        coordinator.configure(installationID: installationID)

        try await coordinator.receiveToken(token)

        XCTAssertEqual(
            registrar.registrations,
            [.init(token: token, kind: .voip, environment: .sandbox, installationID: installationID)]
        )
    }

    func testPreservesTokenForRetryAfterUploadFailure() async throws {
        let registrar = RecordingPushTokenRegistrar(error: TestPushError.failed)
        let coordinator = VoIPPushCoordinator(
            tokenRegistrar: registrar,
            environment: .production,
            incomingPushHandler: { _ in }
        )
        coordinator.configure(installationID: UUID())

        do {
            try await coordinator.receiveToken(Data(repeating: 4, count: 32))
            XCTFail("Expected token upload to fail")
        } catch {
            XCTAssertEqual(error as? TestPushError, .failed)
        }
        registrar.error = nil
        try await coordinator.uploadPendingToken()

        XCTAssertEqual(registrar.registrations.count, 2)
    }

    func testForwardsSerializedEncryptedPayload() async throws {
        var received: Data?
        let coordinator = VoIPPushCoordinator(
            tokenRegistrar: RecordingPushTokenRegistrar(),
            environment: .sandbox
        ) { data in
            received = data
        }

        try await coordinator.receiveIncomingPayload([
            "notification_id": "01900000-0000-7000-8000-000000000010",
            "ciphertext": "opaque",
        ])

        let object = try XCTUnwrap(
            JSONSerialization.jsonObject(with: try XCTUnwrap(received)) as? [String: String]
        )
        XCTAssertEqual(object["ciphertext"], "opaque")
    }

    func testDeletesInvalidatedVoIPTokenForCurrentInstallation() async throws {
        let registrar = RecordingPushTokenRegistrar()
        let installationID = UUID()
        let coordinator = VoIPPushCoordinator(
            tokenRegistrar: registrar,
            environment: .production,
            incomingPushHandler: { _ in }
        )
        coordinator.configure(installationID: installationID)

        try await coordinator.invalidateToken()

        XCTAssertEqual(
            registrar.deletions,
            [.init(kind: .voip, environment: .production, installationID: installationID)]
        )
    }

    func testRetriesDeletionBeforeRegisteringAReplacementToken() async throws {
        let registrar = RecordingPushTokenRegistrar(error: TestPushError.failed)
        let installationID = UUID()
        let coordinator = VoIPPushCoordinator(
            tokenRegistrar: registrar,
            environment: .sandbox,
            incomingPushHandler: { _ in }
        )
        coordinator.configure(installationID: installationID)

        do {
            try await coordinator.invalidateToken()
            XCTFail("Expected token deletion to fail")
        } catch {
            XCTAssertEqual(error as? TestPushError, .failed)
        }
        registrar.error = nil
        try await coordinator.receiveToken(Data(repeating: 8, count: 32))

        XCTAssertEqual(registrar.deletions.count, 2)
        XCTAssertEqual(registrar.registrations.count, 1)
    }
}

@MainActor
private final class RecordingPushTokenRegistrar: PushTokenRegistering {
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

private enum TestPushError: Error, Equatable {
    case failed
}
