// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import AVFAudio
import CryptoKit
import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class LiveCallTransportTests: XCTestCase {
    private let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000001")!
    private let commandID = UUID(uuidString: "01900000-0000-7000-8000-000000000010")!
    private let callID = UUID(uuidString: "018f0000-0000-7000-8000-000000000001")!

    func testCommandEncryptionMatchesGoCompatibilityVector() throws {
        let devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
        let senderPrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((33...64).map(UInt8.init))
        )
        let payload = CallCommandPayload(
            version: 1,
            operation: "call.start",
            callID: callID.uuidString.lowercased(),
            number: "13500000000",
            digits: nil
        )
        let installationIdentity = InstallationIdentity(
            id: UUID(uuidString: "01900000-0000-7000-8000-000000000101")!,
            signingPrivateKey: Data((65...96).map(UInt8.init)),
            keyAgreementPrivateKey: Data((97...128).map(UInt8.init)),
            pendingRevocationIDs: []
        )

        let sealed = try EncryptedCallCommandCodec.seal(
            payload: payload,
            recipientPublicKey: devicePrivateKey.publicKey.rawRepresentation,
            senderPrivateKey: senderPrivateKey,
            nonce: try AES.GCM.Nonce(data: Data(0...11)),
            commandID: commandID,
            deviceID: deviceID,
            createdAt: Date(timeIntervalSince1970: 0),
            installationIdentity: installationIdentity
        )

        XCTAssertEqual(
            sealed.request.senderPublicKey.base64EncodedString(),
            "WGmv9FBUlzLLqu1eXfmzCm2jHLDldCutWtShp2jxpns="
        )
        XCTAssertEqual(
            sealed.request.ciphertext.base64EncodedString(),
            "8C9qlHoP+UOktcg7CVAbJkL0spZi2YtHSmdCbO/BHRvZ6KIEeE9wUPPGWv5l4O8vfga1aK7M/HzWds0hUL0cMWo8RgkMx7SRAg1uSoyJfcgyL84GKPyCaOfCpabajw3u75vmCoUy3i7KrCSeMuyMqHDGUMFtSLT+e+JrSFVu"
        )
        XCTAssertEqual(sealed.request.senderInstallationID, installationIdentity.id)
        let verificationKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: installationIdentity.signingPrivateKey
        ).publicKey
        XCTAssertTrue(
            verificationKey.isValidSignature(
                sealed.request.signature,
                for: EncryptedCallCommandCodec.signedData(
                    for: sealed.request,
                    deviceID: deviceID
                )
            )
        )

        let tamperedRequests = [
            replacing(sealed.request, ciphertext: sealed.request.ciphertext + Data([0xff])),
            replacing(sealed.request, senderInstallationID: UUID()),
            replacing(
                sealed.request,
                createdAt: sealed.request.createdAt.addingTimeInterval(1)
            ),
            replacing(
                sealed.request,
                expiresAt: sealed.request.expiresAt.addingTimeInterval(1)
            ),
        ]
        for request in tamperedRequests {
            XCTAssertFalse(
                verificationKey.isValidSignature(
                    request.signature,
                    for: EncryptedCallCommandCodec.signedData(
                        for: request,
                        deviceID: deviceID
                    )
                )
            )
        }
    }

    func testCallEventDecryptionMatchesGoCompatibilityVector() throws {
        let devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
        let senderPrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((33...64).map(UInt8.init))
        )
        let identityPrivateKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: Data((65...96).map(UInt8.init))
        )
        let sharedSecret = try senderPrivateKey.sharedSecretFromKeyAgreement(
            with: devicePrivateKey.publicKey
        )
        let envelope = EncryptedEventEnvelope(
            id: UUID(uuidString: "01900000-0000-7000-8000-000000000011")!,
            sequence: 0,
            type: "call.state",
            deviceUUID: deviceID,
            occurredAt: Date(timeIntervalSince1970: 1_792_138_150),
            keyEpoch: 1,
            nonce: Data(repeating: 0, count: 12),
            ciphertext: Data(base64Encoded: "r7ZfXWX5iFdtSt7NFmMawU9ANd92JPGaOC9VbXwQ1JfXcNVloVdhs82ohy9chIWeACMGiKQxY6Z6qXHp7ZHqdDAlOIyuOkd4odiqTyBWk2kp/1Xo09yHgiqYroKoTc2ngTF1QJKjhj877JI8joks7dhKZ4hyp4O8NZa4r9YAZ5YbuAv9EOJLnjyO")!,
            signature: Data(base64Encoded: "WxQ9bm1NtcF3JSTvIXwIY27QcxJpqcYeLy+x5Ra9+3m1feoBsBOIIdYwmBlEJsuCV92qKFRlN3V2eiyofIHNCQ==")!
        )

        let payload = try EncryptedCallEventCodec.open(
            envelope,
            sharedSecret: sharedSecret.withUnsafeBytes { Data($0) },
            verificationKey: identityPrivateKey.publicKey.rawRepresentation
        )

        XCTAssertEqual(
            payload,
            CallStateEventPayload(
                version: 1,
                callID: callID,
                state: "active",
                active: true,
                held: false,
                waiting: false
            )
        )
    }

    func testMediaSessionDescriptionsUseTheSharedEncryptedProtocolFields() throws {
        let payload = CallCommandPayload(
            version: 1,
            operation: "call.start",
            callID: callID.uuidString.lowercased(),
            number: "10086",
            mediaOffer: "v=0\r\na=fingerprint:sha-256 phone\r\n"
        )
        let payloadData = try JSONEncoder().encode(payload)
        let payloadObject = try XCTUnwrap(
            JSONSerialization.jsonObject(with: payloadData) as? [String: Any]
        )
        XCTAssertEqual(payloadObject["media_offer"] as? String, payload.mediaOffer)
        XCTAssertNil(payloadObject["mediaOffer"])

        let resultData = Data(
            #"{"version":1,"success":true,"call_state":"dialing","media_answer":"v=0\r\na=fingerprint:sha-256 device\r\n"}"#.utf8
        )
        let result = try JSONDecoder().decode(CallCommandResult.self, from: resultData)
        XCTAssertEqual(result.mediaAnswer, "v=0\r\na=fingerprint:sha-256 device\r\n")
    }

    func testIncomingCallRegistrationAnswersAndRejectsWithEncryptedCommands() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        try transport.registerIncomingCall(
            IncomingCallRegistration(
                callID: callID,
                lineID: deviceID,
                callSecret: Data((97...128).map(UInt8.init)),
                verificationKey: api.verificationKey
            )
        )

        try await transport.answerIncomingCall(callID: callID)
        try await transport.rejectIncomingCall(callID: callID)

        XCTAssertEqual(api.payloads.map(\.operation), ["call.answer", "call.reject"])
        XCTAssertEqual(api.payloads.first?.mediaOffer, media.offer)
        XCTAssertEqual(media.appliedAnswers, [CallMediaSessionFake.answer])
        XCTAssertTrue(media.isClosed)
    }

    func testIncomingCallRegistrationRejectsInvalidSecretsBeforeCommands() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        XCTAssertThrowsError(
            try transport.registerIncomingCall(
                IncomingCallRegistration(
                    callID: callID,
                    lineID: deviceID,
                    callSecret: Data(repeating: 1, count: 31),
                    verificationKey: Data(repeating: 2, count: 32)
                )
            )
        ) { error in
            XCTAssertEqual(error as? LiveCallTransportError, .invalidDeviceKey)
        }
        XCTAssertTrue(api.requests.isEmpty)
    }

    func testIncomingRejectFailureStillDiscardsTheLocalCallContext() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .modemRejected)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            automaticallyPollEvents: false
        )
        try transport.registerIncomingCall(
            IncomingCallRegistration(
                callID: callID,
                lineID: deviceID,
                callSecret: Data((97...128).map(UInt8.init)),
                verificationKey: api.verificationKey
            )
        )

        do {
            try await transport.rejectIncomingCall(callID: callID)
            XCTFail("Expected rejectIncomingCall to fail")
        } catch {
            XCTAssertNotNil(error)
        }
        do {
            try await transport.rejectIncomingCall(callID: callID)
            XCTFail("Expected the local call context to be discarded")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidResponse)
        }
    }

    func testCallControlsMapToEncryptedDeviceOperations() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }

        try await transport.startOutgoingCall(callID: callID, number: "13500000000", lineID: deviceID)
        try await transport.setHeld(true, callID: callID)
        try await transport.setHeld(false, callID: callID)
        try await transport.setMuted(true, callID: callID)
        try await transport.setMuted(false, callID: callID)
        try await transport.playDTMF("#", callID: callID)
        try await transport.endCall(callID: callID)

        XCTAssertEqual(api.payloads.map(\.operation), [
            "call.start", "call.hold", "call.resume", "call.dtmf", "call.end",
        ])
        XCTAssertEqual(api.payloads.first?.number, "13500000000")
        XCTAssertEqual(api.payloads.first?.mediaOffer, media.offer)
        XCTAssertEqual(api.payloads.first?.callLeaseSeconds, 30)
        XCTAssertEqual(api.payloads[3].digits, "#")
        XCTAssertEqual(media.appliedAnswers, [CallMediaSessionFake.answer])
        XCTAssertEqual(media.muteChanges, [true, false])
        XCTAssertTrue(media.isClosed)
        XCTAssertTrue(events.isEmpty)

        for request in api.requests {
            let encoded = try JSONEncoder().encode(request)
            XCTAssertFalse(String(decoding: encoded, as: UTF8.self).contains("13500000000"))
            XCTAssertFalse(String(decoding: encoded, as: UTF8.self).contains("call.start"))
        }
    }

    func testEndFailureStillClosesLocalMediaAndEventState() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        try await transport.startOutgoingCall(callID: callID, number: "13500000000", lineID: deviceID)
        api.outcome = .modemRejected

        do {
            try await transport.endCall(callID: callID)
            XCTFail("Expected endCall to fail")
        } catch {
            XCTAssertNotNil(error)
        }
        XCTAssertTrue(media.isClosed)
        do {
            try await transport.setHeld(true, callID: callID)
            XCTFail("Expected the local call context to be discarded")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidResponse)
        }
    }

    func testOutgoingCommandsCarrySelectedSlotAndSIMIdentity() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000301")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000302")!
        let line = DeviceLineIdentity(
            id: simID,
            deviceID: deviceID,
            slotID: slotID,
            simID: simID,
            numericID: 2,
            deviceName: "EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true,
            commandPublicKey: nil
        )
        transport.updateLineRoutes([line])
        let currentSlotID = UUID(uuidString: "01900000-0000-7000-8000-000000000303")!
        let currentSIMID = UUID(uuidString: "01900000-0000-7000-8000-000000000304")!
        api.refreshedTarget = EncryptedCommandTarget(
            deviceID: deviceID,
            slotID: currentSlotID,
            simID: currentSIMID,
            publicKey: api.commandPublicKey
        )

        try await transport.startOutgoingCall(callID: callID, number: "13500000000", lineID: simID)

        XCTAssertEqual(api.payloads.first?.slotID, currentSlotID)
        XCTAssertEqual(api.payloads.first?.simID, currentSIMID)
    }

    func testEncryptedModemFailureIsSurfacedWithoutLeakingModemDetails() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .modemRejected)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
            XCTFail("Expected the encrypted modem rejection")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .modemRejected("modem_rejected"))
        }

        let encoded = try JSONEncoder().encode(try XCTUnwrap(api.requests.first))
        let serverVisibleBody = String(decoding: encoded, as: UTF8.self)
        XCTAssertFalse(serverVisibleBody.contains("ATD"))
        XCTAssertFalse(serverVisibleBody.contains("modem_rejected"))
    }

    func testTamperedEncryptedResultIsRejected() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .tampered)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
            XCTFail("Expected authentication to fail")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidResponse)
        }
    }

    func testMissingOrInvalidDeviceKeyDoesNotQueueACommand() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        api.recipientKeyOverride = Data(repeating: 1, count: 31)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
            XCTFail("Expected key validation to fail")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidDeviceKey)
        }

        XCTAssertTrue(api.requests.isEmpty)
    }

    func testSignedEncryptedModemEventsDriveTheCallLifecycle() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }
        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        let connectedAt = Date(timeIntervalSince1970: 1_792_108_923)
        try api.appendCallEvent(callID: callID, state: "ringing", occurredAt: connectedAt.addingTimeInterval(-2))
        try api.appendCallEvent(callID: callID, state: "active", occurredAt: connectedAt)
        try api.appendCallEvent(callID: callID, state: "ended", occurredAt: connectedAt.addingTimeInterval(5))

        try await transport.synchronizeEvents()

        XCTAssertEqual(events, [
            .ringing(callID: callID),
            .connected(callID: callID, at: connectedAt),
            .ended(callID: callID, reason: .remoteEnded),
        ])
        XCTAssertEqual(api.requestedEventCursors, [0])
    }

    func testWebSocketEventStreamProcessesThenAcknowledgesTheCursor() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let stream = RecordingEncryptedEventStream()
        let opener = RecordingEncryptedEventStreamOpener { cursor in
            XCTAssertEqual(cursor, 0)
            try api.appendCallEvent(callID: self.callID, state: "ended", occurredAt: .now)
            stream.pages = [
                EncryptedEventPage(events: api.events, nextSequence: 1),
            ]
            return stream
        }
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            eventStreamOpener: opener
        )
        let ended = expectation(description: "Encrypted streamed call event ended the call")
        transport.setEventHandler { event in
            if event == .ended(callID: self.callID, reason: .unanswered) {
                ended.fulfill()
            }
        }

        try await transport.startOutgoingCall(
            callID: callID,
            number: "10086",
            lineID: deviceID
        )
        await fulfillment(of: [ended], timeout: 1)

        XCTAssertEqual(opener.requestedCursors, [0])
        XCTAssertEqual(stream.acknowledgedSequences, [1])
        XCTAssertTrue(stream.isClosed)
    }

    func testActiveCallRenewsItsEncryptedDeviceLeaseUntilItEnds() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let gate = CallLeaseRenewalGate()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            waitBeforeCallLeaseRenewal: { await gate.wait() },
            automaticallyPollEvents: false,
            automaticallyRenewCallLease: true
        )

        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        await gate.signal()
        for _ in 0..<100 where api.payloads.count < 2 {
            await Task.yield()
        }

        XCTAssertEqual(api.payloads.map(\.operation), ["call.start", "call.heartbeat"])
        XCTAssertEqual(api.payloads.last?.callID, callID.uuidString.lowercased())

        try await transport.endCall(callID: callID)
        XCTAssertEqual(api.payloads.map(\.operation), ["call.start", "call.heartbeat", "call.end"])
    }

    func testRepeatedEventStreamFailureClosesMediaAndEndsTheCallAsFailed() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let opener = RecordingEncryptedEventStreamOpener { _ in
            throw ServerConnectionError.eventStreamUnavailable
        }
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyRenewCallLease: false,
            eventStreamOpener: opener
        )
        let ended = expectation(description: "Repeated stream failures end the active call")
        transport.setEventHandler { event in
            if event == .ended(callID: self.callID, reason: .failed) {
                ended.fulfill()
            }
        }

        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        await fulfillment(of: [ended], timeout: 1)
        for _ in 0..<100 where api.payloads.count < 2 {
            await Task.yield()
        }

        XCTAssertEqual(opener.requestedCursors, [0, 0, 0])
        XCTAssertEqual(api.payloads.map(\.operation), ["call.start", "call.end"])
        XCTAssertTrue(media.isClosed)
        do {
            try await transport.setHeld(true, callID: callID)
            XCTFail("Expected failed event-stream cleanup to discard the call")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidResponse)
        }
    }

    func testNetworkSwitchReopensCallEventsWithoutEndingTheCall() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let firstOpened = expectation(description: "first call event stream opened")
        let secondOpened = expectation(description: "call event stream reopened")
        let first = BlockingEncryptedEventStream()
        let second = BlockingEncryptedEventStream()
        var openCount = 0
        let opener = RecordingEncryptedEventStreamOpener { _ in
            openCount += 1
            if openCount == 1 {
                firstOpened.fulfill()
                return first
            }
            secondOpened.fulfill()
            return second
        }
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            automaticallyRenewCallLease: false,
            eventStreamOpener: opener
        )
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }

        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        await fulfillment(of: [firstOpened], timeout: 1)
        transport.networkPathDidChange(isAvailable: false)
        XCTAssertTrue(first.isClosed)
        XCTAssertTrue(events.isEmpty)
        XCTAssertFalse(media.isClosed)
        XCTAssertEqual(opener.requestedCursors, [0])

        transport.networkPathDidChange(isAvailable: true)
        await fulfillment(of: [secondOpened], timeout: 1)
        XCTAssertEqual(opener.requestedCursors, [0, 0])
        XCTAssertTrue(events.isEmpty)
        XCTAssertFalse(media.isClosed)
        await transport.reset()
    }

    func testLateOldCallEventConnectionDoesNotCloseReopenedStream() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let firstOpening = expectation(description: "first call event connection opening")
        let secondOpened = expectation(description: "second call event connection opened")
        let staleClosed = expectation(description: "stale call event connection closed")
        let stale = BlockingEncryptedEventStream(onClose: { staleClosed.fulfill() })
        let current = BlockingEncryptedEventStream()
        var pendingOpen: CheckedContinuation<any EncryptedEventStream, Never>?
        var attempts = 0
        let opener = RecordingEncryptedEventStreamOpener { _ in
            attempts += 1
            if attempts == 1 {
                return await withCheckedContinuation { continuation in
                    pendingOpen = continuation
                    firstOpening.fulfill()
                }
            }
            secondOpened.fulfill()
            return current
        }
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            automaticallyRenewCallLease: false,
            eventStreamOpener: opener
        )
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }

        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        await fulfillment(of: [firstOpening], timeout: 1)
        transport.networkPathDidChange(isAvailable: false)
        transport.networkPathDidChange(isAvailable: true)
        await fulfillment(of: [secondOpened], timeout: 1)
        pendingOpen?.resume(returning: stale)
        await fulfillment(of: [staleClosed], timeout: 1)

        XCTAssertFalse(current.isClosed)
        XCTAssertFalse(media.isClosed)
        XCTAssertTrue(events.isEmpty)
        await transport.reset()
    }

    func testTamperedOrWrongCallEventsAreNotAccepted() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }
        try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
        try api.appendCallEvent(callID: UUID(), state: "active", occurredAt: .now)
        try api.appendCallEvent(callID: callID, state: "active", occurredAt: .now, tampered: true)

        try await transport.synchronizeEvents()

        XCTAssertTrue(events.isEmpty)
    }

    func testMissingMediaAnswerClosesLocalMediaAndEndsRemoteCall() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        api.omitMediaAnswer = true
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(
                callID: callID,
                number: "10086",
                lineID: deviceID
            )
            XCTFail("Expected the missing media answer to fail")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidResponse)
        }

        XCTAssertEqual(api.payloads.map(\.operation), ["call.start", "call.end"])
        XCTAssertTrue(media.isClosed)
        XCTAssertTrue(media.appliedAnswers.isEmpty)
    }

    func testInvalidMediaAnswerClosesLocalMediaAndEndsRemoteCall() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        media.applyAnswerError = CallMediaSessionError.invalidAnswer
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(
                callID: callID,
                number: "10086",
                lineID: deviceID
            )
            XCTFail("Expected the invalid media answer to fail")
        } catch {
            XCTAssertEqual(error as? CallMediaSessionError, .invalidAnswer)
        }

        XCTAssertEqual(api.payloads.map(\.operation), ["call.start", "call.end"])
        XCTAssertTrue(media.isClosed)
    }

    func testOutgoingMediaReceivesEarlierCallKitAudioActivation() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        let audioSession = AVAudioSession.sharedInstance()

        await transport.audioSessionDidActivate(audioSession)
        try await transport.startOutgoingCall(
            callID: callID,
            number: "13500000000",
            lineID: deviceID
        )

        XCTAssertEqual(media.activatedAudioSessions, [ObjectIdentifier(audioSession)])
        await transport.audioSessionDidDeactivate(audioSession)
        XCTAssertEqual(media.deactivatedAudioSessions, [ObjectIdentifier(audioSession)])
    }

    func testIncomingMediaReceivesEarlierCallKitAudioActivation() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
            commandSigner: api.commandSigner,
            makeMediaSession: { media },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )
        try transport.registerIncomingCall(
            IncomingCallRegistration(
                callID: callID,
                lineID: deviceID,
                callSecret: Data((97...128).map(UInt8.init)),
                verificationKey: api.verificationKey
            )
        )
        let audioSession = AVAudioSession.sharedInstance()

        await transport.audioSessionDidActivate(audioSession)
        try await transport.answerIncomingCall(callID: callID)

        XCTAssertEqual(media.activatedAudioSessions, [ObjectIdentifier(audioSession)])
    }

    func testMissingInstallationIdentityDoesNotQueueACommand() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(
            api: api,
            commandSigner: InstallationCommandSigner(),
            makeMediaSession: { CallMediaSessionFake() },
            waitBeforePollingAgain: {},
            automaticallyPollEvents: false
        )

        do {
            try await transport.startOutgoingCall(
                callID: callID,
                number: "10086",
                lineID: deviceID
            )
            XCTFail("Expected installation identity validation to fail")
        } catch {
            XCTAssertEqual(
                error as? LiveCallTransportError,
                .missingInstallationIdentity
            )
        }

        XCTAssertTrue(api.requests.isEmpty)
    }

    private func replacing(
        _ request: EncryptedCallCommandRequest,
        senderInstallationID: UUID? = nil,
        ciphertext: Data? = nil,
        createdAt: Date? = nil,
        expiresAt: Date? = nil
    ) -> EncryptedCallCommandRequest {
        EncryptedCallCommandRequest(
            id: request.id,
            senderInstallationID: senderInstallationID ?? request.senderInstallationID,
            senderPublicKey: request.senderPublicKey,
            nonce: request.nonce,
            ciphertext: ciphertext ?? request.ciphertext,
            signature: request.signature,
            createdAt: createdAt ?? request.createdAt,
            expiresAt: expiresAt ?? request.expiresAt
        )
    }
}

@MainActor
private final class RecordingEncryptedEventStreamOpener: EncryptedEventStreamOpening {
    private let handler: @MainActor (Int64) async throws -> any EncryptedEventStream
    private(set) var requestedCursors: [Int64] = []

    init(handler: @escaping @MainActor (Int64) async throws -> any EncryptedEventStream) {
        self.handler = handler
    }

    func openEncryptedEventStream(afterSequence: Int64) async throws -> any EncryptedEventStream {
        requestedCursors.append(afterSequence)
        return try await handler(afterSequence)
    }
}

@MainActor
private final class RecordingEncryptedEventStream: EncryptedEventStream {
    var pages: [EncryptedEventPage] = []
    private(set) var acknowledgedSequences: [Int64] = []
    private(set) var isClosed = false

    func receive() async throws -> EncryptedEventPage {
        guard !pages.isEmpty else {
            throw ServerConnectionError.eventStreamUnavailable
        }
        return pages.removeFirst()
    }

    func acknowledge(sequence: Int64) async throws {
        acknowledgedSequences.append(sequence)
    }

    func close() {
        isClosed = true
    }
}

@MainActor
private final class BlockingEncryptedEventStream: EncryptedEventStream {
    private(set) var isClosed = false
    private let onClose: () -> Void

    init(onClose: @escaping () -> Void = {}) {
        self.onClose = onClose
    }

    func receive() async throws -> EncryptedEventPage {
        try await Task.sleep(for: .seconds(60))
        throw CancellationError()
    }

    func acknowledge(sequence: Int64) async throws {}

    func close() {
        isClosed = true
        onClose()
    }
}

@MainActor
private final class CallMediaSessionFake: CallMediaSession {
    static let answer = "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 0\r\na=rtpmap:0 PCMU/8000\r\n"

    let offer = "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 0\r\na=rtpmap:0 PCMU/8000\r\n"
    var applyAnswerError: Error?
    private(set) var appliedAnswers: [String] = []
    private(set) var muteChanges: [Bool] = []
    private(set) var activatedAudioSessions: [ObjectIdentifier] = []
    private(set) var deactivatedAudioSessions: [ObjectIdentifier] = []
    private(set) var isClosed = false

    func makeOffer() async throws -> String {
        offer
    }

    func applyAnswer(_ answer: String) async throws {
        if let applyAnswerError {
            throw applyAnswerError
        }
        appliedAnswers.append(answer)
    }

    func setMuted(_ isMuted: Bool) {
        muteChanges.append(isMuted)
    }

    func audioSessionDidActivate(_ audioSession: AVAudioSession) {
        activatedAudioSessions.append(ObjectIdentifier(audioSession))
    }

    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) {
        deactivatedAudioSessions.append(ObjectIdentifier(audioSession))
    }

    func close() {
        isClosed = true
    }
}

private actor CallLeaseRenewalGate {
    private var permits = 0
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        if permits > 0 {
            permits -= 1
            return
        }
        await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }

    func signal() {
        guard !waiters.isEmpty else {
            permits += 1
            return
        }
        waiters.removeFirst().resume()
    }
}

@MainActor
private final class DeviceCommandAPIFake: EncryptedCallCommandAPI {
    enum Outcome {
        case success
        case modemRejected
        case tampered
    }

    private let deviceID: UUID
    private let devicePrivateKey: Curve25519.KeyAgreement.PrivateKey
    private let identityPrivateKey: Curve25519.Signing.PrivateKey
    private let installationIdentity: InstallationIdentity
    var outcome: Outcome
    private var results: [UUID: EncryptedCallCommandResult] = [:]
    private var callSharedSecret: SharedSecret?

    var recipientKeyOverride: Data?
    var refreshedTarget: EncryptedCommandTarget?
    var omitMediaAnswer = false
    private(set) var requests: [EncryptedCallCommandRequest] = []
    private(set) var payloads: [CallCommandPayload] = []
    private(set) var events: [EncryptedEventEnvelope] = []
    private(set) var requestedEventCursors: [Int64] = []

    var verificationKey: Data { identityPrivateKey.publicKey.rawRepresentation }
    var commandPublicKey: Data { devicePrivateKey.publicKey.rawRepresentation }
    var commandSigner: InstallationCommandSigner {
        InstallationCommandSigner(identity: installationIdentity)
    }

    init(deviceID: UUID, outcome: Outcome) throws {
        self.deviceID = deviceID
        self.outcome = outcome
        devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
        identityPrivateKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: Data((65...96).map(UInt8.init))
        )
        installationIdentity = InstallationIdentity(
            id: UUID(uuidString: "01900000-0000-7000-8000-000000000101")!,
            signingPrivateKey: Data((129...160).map(UInt8.init)),
            keyAgreementPrivateKey: Data((161...192).map(UInt8.init)),
            pendingRevocationIDs: []
        )
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return recipientKeyOverride ?? devicePrivateKey.publicKey.rawRepresentation
    }

    func commandTarget(for line: DeviceLineIdentity) async throws -> EncryptedCommandTarget {
        XCTAssertEqual(line.deviceID, deviceID)
        return refreshedTarget ?? EncryptedCommandTarget(
            deviceID: deviceID,
            slotID: line.slotID,
            simID: line.simID,
            publicKey: recipientKeyOverride ?? commandPublicKey
        )
    }

    func eventVerificationKey(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return identityPrivateKey.publicKey.rawRepresentation
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        XCTAssertEqual(deviceID, self.deviceID)
        let verificationKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: installationIdentity.signingPrivateKey
        ).publicKey
        XCTAssertEqual(command.senderInstallationID, installationIdentity.id)
        XCTAssertTrue(
            verificationKey.isValidSignature(
                command.signature,
                for: EncryptedCallCommandCodec.signedData(
                    for: command,
                    deviceID: deviceID
                )
            )
        )
        requests.append(command)

        let senderPublicKey = try Curve25519.KeyAgreement.PublicKey(
            rawRepresentation: command.senderPublicKey
        )
        let sharedSecret = try devicePrivateKey.sharedSecretFromKeyAgreement(with: senderPublicKey)
        if payloads.count == 0 {
            callSharedSecret = sharedSecret
        }
        let commandKey = Self.key(
            sharedSecret: sharedSecret,
            commandID: command.id,
            deviceID: deviceID,
            context: "sim-deck-command-v1"
        )
        let commandBox = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: command.nonce),
            ciphertext: Data(command.ciphertext.dropLast(16)),
            tag: Data(command.ciphertext.suffix(16))
        )
        let plaintext = try AES.GCM.open(
            commandBox,
            using: commandKey,
            authenticating: Self.associatedData(
                context: "sim-deck-command-v1",
                commandID: command.id,
                deviceID: deviceID
            )
        )
        let payload = try JSONDecoder().decode(CallCommandPayload.self, from: plaintext)
        payloads.append(payload)

        let result = CallCommandResult(
            version: 1,
            success: outcome != .modemRejected,
            errorCode: outcome == .modemRejected ? "modem_rejected" : nil,
            callState: "dialing",
            mediaAnswer: ["call.start", "call.answer"].contains(payload.operation) && !omitMediaAnswer
                ? CallMediaSessionFake.answer
                : nil
        )
        let resultKey = Self.key(
            sharedSecret: sharedSecret,
            commandID: command.id,
            deviceID: deviceID,
            context: "sim-deck-command-result-v1"
        )
        let nonce = try AES.GCM.Nonce(data: Data(repeating: 9, count: 12))
        let sealed = try AES.GCM.seal(
            JSONEncoder().encode(result),
            using: resultKey,
            nonce: nonce,
            authenticating: Self.associatedData(
                context: "sim-deck-command-result-v1",
                commandID: command.id,
                deviceID: deviceID
            )
        )
        var ciphertext = sealed.ciphertext + sealed.tag
        if outcome == .tampered {
            var bytes = [UInt8](ciphertext)
            bytes[0] ^= 0xff
            ciphertext = Data(bytes)
        }
        results[command.id] = EncryptedCallCommandResult(
            nonce: nonce.withUnsafeBytes { Data($0) },
            ciphertext: ciphertext
        )
    }

    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus {
        EncryptedCallCommandStatus(
            id: commandID,
            deviceUUID: deviceID,
            state: "completed",
            result: results[commandID]
        )
    }

    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage {
        requestedEventCursors.append(afterSequence)
        let pageEvents = events.filter { $0.sequence > afterSequence }
        return EncryptedEventPage(
            events: pageEvents,
            nextSequence: pageEvents.last?.sequence ?? afterSequence
        )
    }

    func appendCallEvent(
        callID: UUID,
        state: String,
        occurredAt: Date,
        tampered: Bool = false
    ) throws {
        let sharedSecret = try XCTUnwrap(callSharedSecret)
        let eventID = UUID()
        let sequence = Int64(events.count + 1)
        let payload = CallStateEventPayload(
            version: 1,
            callID: callID,
            state: state,
            active: state == "active",
            held: state == "held",
            waiting: state == "waiting"
        )
        var envelope = EncryptedEventEnvelope(
            id: eventID,
            sequence: sequence,
            type: "call.state",
            deviceUUID: deviceID,
            occurredAt: occurredAt,
            keyEpoch: 1,
            nonce: Data(repeating: UInt8(sequence), count: 12),
            ciphertext: Data(),
            signature: Data()
        )
        let key = sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(eventID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data("sim-deck-call-event-v1".utf8),
            outputByteCount: 32
        )
        let nonce = try AES.GCM.Nonce(data: envelope.nonce)
        let sealed = try AES.GCM.seal(
            JSONEncoder().encode(payload),
            using: key,
            nonce: nonce,
            authenticating: EncryptedCallEventCodec.associatedData(envelope)
        )
        envelope = EncryptedEventEnvelope(
            id: envelope.id,
            sequence: envelope.sequence,
            type: envelope.type,
            deviceUUID: envelope.deviceUUID,
            occurredAt: envelope.occurredAt,
            keyEpoch: envelope.keyEpoch,
            nonce: envelope.nonce,
            ciphertext: sealed.ciphertext + sealed.tag,
            signature: Data()
        )
        var signature = try identityPrivateKey.signature(
            for: EncryptedCallEventCodec.signedData(envelope)
        )
        if tampered {
            signature[signature.startIndex] ^= 0xff
        }
        events.append(EncryptedEventEnvelope(
            id: envelope.id,
            sequence: envelope.sequence,
            type: envelope.type,
            deviceUUID: envelope.deviceUUID,
            occurredAt: envelope.occurredAt,
            keyEpoch: envelope.keyEpoch,
            nonce: envelope.nonce,
            ciphertext: envelope.ciphertext,
            signature: signature
        ))
    }

    private static func key(
        sharedSecret: SharedSecret,
        commandID: UUID,
        deviceID: UUID,
        context: String
    ) -> SymmetricKey {
        sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data(context.utf8),
            outputByteCount: 32
        )
    }

    private static func associatedData(context: String, commandID: UUID, deviceID: UUID) -> Data {
        Data("\(context)\0\(commandID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8)
    }
}
