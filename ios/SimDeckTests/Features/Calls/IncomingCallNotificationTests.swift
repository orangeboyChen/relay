// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

final class IncomingCallNotificationTests: XCTestCase {
    func testClassifiesIncomingAndAnsweredElsewhereDeliveries() throws {
        guard case .incoming(let incoming) = try IncomingCallPushDelivery.decode(Self.deliveryData) else {
            return XCTFail("Expected an incoming call delivery")
        }
        XCTAssertEqual(
            incoming.notificationID,
            UUID(uuidString: "11121314-1516-4718-991a-1b1c1d1e1f20")
        )

        let notificationID = UUID()
        let claimData = Data(
            """
            {"notification_id":"\(notificationID.uuidString.lowercased())","reason":"answered_elsewhere","aps":{"content-available":1}}
            """.utf8
        )
        XCTAssertEqual(
            try IncomingCallPushDelivery.decode(claimData),
            .answeredElsewhere(notificationID: notificationID)
        )
    }

    func testRejectsUnknownVoIPPushDeliveries() {
        XCTAssertThrowsError(
            try IncomingCallPushDelivery.decode(
                Data(#"{"notification_id":"11121314-1516-4718-991a-1b1c1d1e1f20","reason":"unknown"}"#.utf8)
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .invalidEnvelope)
        }
    }

    private let installationID = UUID(
        uuidString: "01900000-0000-7000-8000-000000000101"
    )!
    private let recipientPrivateKey = Data(
        base64Encoded: "ISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0A="
    )!
    private let devicePublicKey = Data(
        base64Encoded: "rcFAEfgtHFbZVqpPnXPYhYNhpgYEhSXg0Ixjjcdd2Mc="
    )!

    func testOpensDeviceGeneratedCompatibilityVector() throws {
        let opened = try IncomingCallNotificationCodec.open(
            Self.deliveryData,
            installationIdentity: identity(),
            deviceVerificationKey: devicePublicKey
        )

        XCTAssertEqual(
            opened.delivery.notificationID,
            UUID(uuidString: "11121314-1516-4718-991a-1b1c1d1e1f20")
        )
        XCTAssertEqual(
            opened.payload.callID,
            UUID(uuidString: "01020304-0506-4708-890a-0b0c0d0e0f10")
        )
        XCTAssertEqual(
            opened.payload.deviceUUID,
            UUID(uuidString: "01900000-0000-7000-8000-000000000001")
        )
        XCTAssertEqual(opened.payload.numericID, 2)
        XCTAssertEqual(opened.payload.remoteNumber, "+8613500000000")
        XCTAssertEqual(opened.payload.state, "ringing")
        XCTAssertEqual(opened.payload.callSecret, recipientPrivateKey)
    }

    func testDecodesSlotAndSIMIdentityFromEncryptedPayloadShape() throws {
        let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000002")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000003")!
        let data = Data(
            """
            {
              "version":1,
              "call_id":"01020304-0506-4708-890a-0b0c0d0e0f10",
              "device_uuid":"01900000-0000-7000-8000-000000000001",
              "slot_id":"\(slotID.uuidString.lowercased())",
              "sim_id":"\(simID.uuidString.lowercased())",
              "numeric_id":2,
              "remote_number":"+8613500000000",
              "state":"ringing",
              "call_secret":"ISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0A=",
              "notification_id":"11121314-1516-4718-991a-1b1c1d1e1f20"
            }
            """.utf8
        )

        let payload = try JSONDecoder().decode(IncomingCallPayload.self, from: data)

        XCTAssertEqual(payload.slotID, slotID)
        XCTAssertEqual(payload.simID, simID)
    }

    func testResolvesColdStartLineFromSignedSlotAndCachedDeviceKeys() throws {
        let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
        let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000002")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000003")!
        let payload = try JSONDecoder().decode(
            IncomingCallPayload.self,
            from: Data(
                """
                {
                  "version":1,
                  "call_id":"01020304-0506-4708-890a-0b0c0d0e0f10",
                  "device_uuid":"\(deviceID.uuidString.lowercased())",
                  "slot_id":"\(slotID.uuidString.lowercased())",
                  "sim_id":"\(simID.uuidString.lowercased())",
                  "numeric_id":2,
                  "remote_number":"+8613500000000",
                  "state":"ringing",
                  "call_secret":"ISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0A=",
                  "notification_id":"11121314-1516-4718-991a-1b1c1d1e1f20"
                }
                """.utf8
            )
        )
        let keys = CachedDeviceKeys(
            verificationKey: Data(repeating: 1, count: 32),
            commandKey: Data(repeating: 2, count: 32)
        )

        let line = try XCTUnwrap(
            IncomingCallLineResolver.resolve(
                payload: payload,
                lines: [],
                cachedDeviceKeys: keys
            )
        )

        XCTAssertEqual(line.id, slotID)
        XCTAssertEqual(line.deviceID, deviceID)
        XCTAssertEqual(line.slotID, slotID)
        XCTAssertEqual(line.simID, simID)
        XCTAssertEqual(line.numericID, 2)
        XCTAssertEqual(line.commandPublicKey, keys.commandKey)
    }

    func testNotificationSharedStateReadsOnlyCompleteCachedDeviceKeys() throws {
        let suiteName = "IncomingCallNotificationTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        let deviceID = UUID()
        let keys = CachedDeviceKeys(
            verificationKey: Data(repeating: 1, count: 32),
            commandKey: Data(repeating: 2, count: 32)
        )

        NotificationSharedState.cacheDeviceKeys([deviceID: keys], defaults: defaults)

        XCTAssertEqual(
            NotificationSharedState.deviceKeys(for: deviceID, defaults: defaults)?.verificationKey,
            keys.verificationKey
        )
        XCTAssertNil(NotificationSharedState.deviceKeys(for: UUID(), defaults: defaults))
    }

    func testRejectsDeliveryForAnotherInstallation() {
        var otherIdentity = identity()
        otherIdentity = InstallationIdentity(
            id: UUID(),
            signingPrivateKey: otherIdentity.signingPrivateKey,
            keyAgreementPrivateKey: otherIdentity.keyAgreementPrivateKey,
            pendingRevocationIDs: []
        )

        XCTAssertThrowsError(
            try IncomingCallNotificationCodec.open(
                Self.deliveryData,
                installationIdentity: otherIdentity,
                deviceVerificationKey: devicePublicKey
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .invalidEnvelope)
        }
    }

    func testRejectsTamperedCiphertextBeforeDecryption() throws {
        var object = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Self.deliveryData) as? [String: Any]
        )
        var recipient = try XCTUnwrap(object["recipient"] as? [String: Any])
        recipient["ciphertext"] = Data(repeating: 9, count: 48).base64EncodedString()
        object["recipient"] = recipient
        let tampered = try JSONSerialization.data(withJSONObject: object)

        XCTAssertThrowsError(
            try IncomingCallNotificationCodec.open(
                tampered,
                installationIdentity: identity(),
                deviceVerificationKey: devicePublicKey
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .invalidSignature)
        }
    }

    func testRejectsWrongDeviceVerificationKey() {
        XCTAssertThrowsError(
            try IncomingCallNotificationCodec.open(
                Self.deliveryData,
                installationIdentity: identity(),
                deviceVerificationKey: Data(repeating: 4, count: 32)
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .invalidSignature)
        }
    }

    func testRejectsMalformedJSONAndPrivateKey() {
        XCTAssertThrowsError(
            try IncomingCallNotificationCodec.open(
                Data("{".utf8),
                installationIdentity: identity(),
                deviceVerificationKey: devicePublicKey
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .invalidEnvelope)
        }

        let invalidIdentity = InstallationIdentity(
            id: installationID,
            signingPrivateKey: Data(repeating: 1, count: 32),
            keyAgreementPrivateKey: Data(repeating: 2, count: 31),
            pendingRevocationIDs: []
        )
        XCTAssertThrowsError(
            try IncomingCallNotificationCodec.open(
                Self.deliveryData,
                installationIdentity: invalidIdentity,
                deviceVerificationKey: devicePublicKey
            )
        ) { error in
            XCTAssertEqual(error as? IncomingCallNotificationError, .decryptionFailed)
        }
    }

    private func identity() -> InstallationIdentity {
        InstallationIdentity(
            id: installationID,
            signingPrivateKey: Data(repeating: 1, count: 32),
            keyAgreementPrivateKey: recipientPrivateKey,
            pendingRevocationIDs: []
        )
    }

    private static let deliveryData = Data(
        #"{"notification_id":"11121314-1516-4718-991a-1b1c1d1e1f20","type":"call.incoming","device_uuid":"01900000-0000-7000-8000-000000000001","occurred_at":"2026-10-16T08:09:10.123Z","key_epoch":1,"recipient":{"installation_id":"01900000-0000-7000-8000-000000000101","sender_public_key":"6loeriZ7jfIm0Xfw4J1pHpKgJanIZvS4FotiTc4LDj4=","nonce":"QUJDREVGR0hJSktM","ciphertext":"Xi7SgnOBl92oPHb/+e6cc8QWwVwhQPm+wLO0VjZOlUrviM6MdE6YwB8IheKox0svfyOFnjvb5BZ+ymoFJAkUqOP2UAsFrbi8fcciW+BCV79XTNCcb4LhY8AMCZeYRfDx87HsE9AlOrk6PU47ewhBclewahT49t6xnMZgpe62Zj7B38VjbD4gfMY9OD9pVGNR2b0dkM7BlDDubPGQMrqivuoXADmOunCOaiO4XeRQt8lPTGTnht3+WAOuJLtG1aGb4c5tAkSilspZUm1Kh2q7ZPg5LW7yMmPUboh3VGP5z6L7hNNdaFbBv7VY+Tre13MtpBFxaT5k4qT+dprX6LBNpmuce13zEh8+7CNFGSyvhI53GbQtvr1aYZBThbPK9jXqGq/6Xx8UUkDUTLgjGK8jalDPRFu/JiYKpdJy","signature":"7bouCuAukIqP3Q38EjgZUV+2v/UhGE7LnsU4imwHCsx0jmP7hDTPUnnYjGqe6vcIR1ZFQkhfPwmx6dOU4eXHAA=="}}"#.utf8
    )
}
