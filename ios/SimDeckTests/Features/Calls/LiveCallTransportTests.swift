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

        let sealed = try EncryptedCallCommandCodec.seal(
            payload: payload,
            recipientPublicKey: devicePrivateKey.publicKey.rawRepresentation,
            senderPrivateKey: senderPrivateKey,
            nonce: try AES.GCM.Nonce(data: Data(0...11)),
            commandID: commandID,
            deviceID: deviceID,
            createdAt: Date(timeIntervalSince1970: 0)
        )

        XCTAssertEqual(
            sealed.request.senderPublicKey.base64EncodedString(),
            "WGmv9FBUlzLLqu1eXfmzCm2jHLDldCutWtShp2jxpns="
        )
        XCTAssertEqual(
            sealed.request.ciphertext.base64EncodedString(),
            "8C9qlHoP+UOktcg7CVAbJkL0spZi2YtHSmdCbO/BHRvZ6KIEeE9wUPPGWv5l4O8vfga1aK7M/HzWds0hUL0cMWo8RgkMx7SRAg1uSoyJfcgyL84GKPyCaOfCpabajw3u75vmCoUy3i7KrCSeMuyMqHDGUMFtSLT+e+JrSFVu"
        )
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

    func testCallControlsMapToEncryptedDeviceOperations() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let media = CallMediaSessionFake()
        let transport = LiveCallTransport(
            api: api,
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

    func testEncryptedModemFailureIsSurfacedWithoutLeakingModemDetails() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .modemRejected)
        let transport = LiveCallTransport(
            api: api,
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

    func testTamperedOrWrongCallEventsAreNotAccepted() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(
            api: api,
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
}

@MainActor
private final class CallMediaSessionFake: CallMediaSession {
    static let answer = "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 0\r\na=rtpmap:0 PCMU/8000\r\n"

    let offer = "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 0\r\na=rtpmap:0 PCMU/8000\r\n"
    var applyAnswerError: Error?
    private(set) var appliedAnswers: [String] = []
    private(set) var muteChanges: [Bool] = []
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

    func audioSessionDidActivate(_ audioSession: AVAudioSession) {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) {}

    func close() {
        isClosed = true
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
    private let outcome: Outcome
    private var results: [UUID: EncryptedCallCommandResult] = [:]
    private var callSharedSecret: SharedSecret?

    var recipientKeyOverride: Data?
    var omitMediaAnswer = false
    private(set) var requests: [EncryptedCallCommandRequest] = []
    private(set) var payloads: [CallCommandPayload] = []
    private(set) var events: [EncryptedEventEnvelope] = []
    private(set) var requestedEventCursors: [Int64] = []

    var verificationKey: Data { identityPrivateKey.publicKey.rawRepresentation }

    init(deviceID: UUID, outcome: Outcome) throws {
        self.deviceID = deviceID
        self.outcome = outcome
        devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
        identityPrivateKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: Data((65...96).map(UInt8.init))
        )
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return recipientKeyOverride ?? devicePrivateKey.publicKey.rawRepresentation
    }

    func eventVerificationKey(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return identityPrivateKey.publicKey.rawRepresentation
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        XCTAssertEqual(deviceID, self.deviceID)
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
