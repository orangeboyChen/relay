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

    func testCallControlsMapToEncryptedDeviceOperations() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = LiveCallTransport(api: api, waitBeforePollingAgain: {})
        var events: [CallTransportEvent] = []
        transport.setEventHandler { events.append($0) }

        try await transport.startOutgoingCall(callID: callID, number: "13500000000", lineID: deviceID)
        try await transport.setHeld(true, callID: callID)
        try await transport.setHeld(false, callID: callID)
        try await transport.playDTMF("#", callID: callID)
        try await transport.endCall(callID: callID)

        XCTAssertEqual(api.payloads.map(\.operation), [
            "call.start", "call.hold", "call.resume", "call.dtmf", "call.end",
        ])
        XCTAssertEqual(api.payloads.first?.number, "13500000000")
        XCTAssertEqual(api.payloads[3].digits, "#")
        XCTAssertEqual(events, [
            .ringing(callID: callID),
            .ended(callID: callID, reason: .remoteEnded),
        ])

        for request in api.requests {
            let encoded = try JSONEncoder().encode(request)
            XCTAssertFalse(String(decoding: encoded, as: UTF8.self).contains("13500000000"))
            XCTAssertFalse(String(decoding: encoded, as: UTF8.self).contains("call.start"))
        }
    }

    func testEncryptedModemFailureIsSurfacedWithoutLeakingModemDetails() async throws {
        let api = try DeviceCommandAPIFake(deviceID: deviceID, outcome: .modemRejected)
        let transport = LiveCallTransport(api: api, waitBeforePollingAgain: {})

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
        let transport = LiveCallTransport(api: api, waitBeforePollingAgain: {})

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
        let transport = LiveCallTransport(api: api, waitBeforePollingAgain: {})

        do {
            try await transport.startOutgoingCall(callID: callID, number: "10086", lineID: deviceID)
            XCTFail("Expected key validation to fail")
        } catch {
            XCTAssertEqual(error as? LiveCallTransportError, .invalidDeviceKey)
        }

        XCTAssertTrue(api.requests.isEmpty)
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
    private let outcome: Outcome
    private var results: [UUID: EncryptedCallCommandResult] = [:]

    var recipientKeyOverride: Data?
    private(set) var requests: [EncryptedCallCommandRequest] = []
    private(set) var payloads: [CallCommandPayload] = []

    init(deviceID: UUID, outcome: Outcome) throws {
        self.deviceID = deviceID
        self.outcome = outcome
        devicePrivateKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: Data((1...32).map(UInt8.init))
        )
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return recipientKeyOverride ?? devicePrivateKey.publicKey.rawRepresentation
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        XCTAssertEqual(deviceID, self.deviceID)
        requests.append(command)

        let senderPublicKey = try Curve25519.KeyAgreement.PublicKey(
            rawRepresentation: command.senderPublicKey
        )
        let sharedSecret = try devicePrivateKey.sharedSecretFromKeyAgreement(with: senderPublicKey)
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
        payloads.append(try JSONDecoder().decode(CallCommandPayload.self, from: plaintext))

        let result = CallCommandResult(
            version: 1,
            success: outcome != .modemRejected,
            errorCode: outcome == .modemRejected ? "modem_rejected" : nil,
            callState: "dialing"
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
