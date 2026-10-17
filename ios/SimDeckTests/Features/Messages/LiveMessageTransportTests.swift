import CryptoKit
import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class LiveMessageTransportTests: XCTestCase {
    private let deviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000201")!
    private let messageID = UUID(uuidString: "01900000-0000-7000-8000-000000000202")!

    func testSendEncryptsAndSignsUnicodeMessageWithoutServerVisibleContent() async throws {
        let api = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = makeTransport(api: api)
        let body = String(repeating: "深圳短信🙂", count: 48)

        try await transport.send(
            messageID: messageID,
            lineID: deviceID,
            remoteNumber: "+8613500000000",
            body: body
        )

        XCTAssertEqual(
            api.payloads,
            [
                MessageCommandPayload(
                    version: 1,
                    operation: "message.send",
                    messageID: messageID.uuidString.lowercased(),
                    number: "+8613500000000",
                    body: body
                ),
            ]
        )
        let request = try XCTUnwrap(api.requests.first)
        XCTAssertEqual(request.senderInstallationID, api.installationIdentity.id)
        XCTAssertTrue(api.installationVerificationKey.isValidSignature(
            request.signature,
            for: EncryptedCallCommandCodec.signedData(for: request, deviceID: deviceID)
        ))

        let serverVisibleRequest = String(
            decoding: try JSONEncoder().encode(request),
            as: UTF8.self
        )
        XCTAssertFalse(serverVisibleRequest.contains("+8613500000000"))
        XCTAssertFalse(serverVisibleRequest.contains("深圳短信"))
        XCTAssertFalse(serverVisibleRequest.contains("message.send"))
    }

    func testSendSurfacesEncryptedModemRejectionAndTampering() async throws {
        let cases: [(MessageCommandAPIFake.Outcome, LiveMessageTransportError)] = [
            (.modemRejected, .modemRejected("modem_rejected")),
            (.tamperedResult, .invalidResponse),
        ]
        for (outcome, expectedError) in cases {
            let api = try MessageCommandAPIFake(deviceID: deviceID, outcome: outcome)
            let transport = makeTransport(api: api)

            do {
                try await transport.send(
                    messageID: messageID,
                    lineID: deviceID,
                    remoteNumber: "10086",
                    body: "test"
                )
                XCTFail("Expected encrypted send failure")
            } catch {
                XCTAssertEqual(error as? LiveMessageTransportError, expectedError)
            }
        }
    }

    func testSendCarriesSelectedSlotAndSIMIdentity() async throws {
        let api = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        let transport = makeTransport(api: api)
        let slotID = UUID(uuidString: "01900000-0000-7000-8000-000000000303")!
        let simID = UUID(uuidString: "01900000-0000-7000-8000-000000000304")!
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
            commandPublicKey: api.recipientKey
        )
        transport.updateLineRoutes([line])

        try await transport.send(
            messageID: messageID,
            lineID: simID,
            remoteNumber: "13500000000",
            body: "test"
        )

        XCTAssertEqual(api.payloads.first?.slotID, slotID)
        XCTAssertEqual(api.payloads.first?.simID, simID)
    }

    func testSendRejectsMissingIdentityInvalidDeviceKeyAndTimeout() async throws {
        let missingIdentityAPI = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        let missingIdentityTransport = LiveMessageTransport(
            api: missingIdentityAPI,
            commandSigner: InstallationCommandSigner(),
            waitBeforePollingAgain: {}
        )
        await assertSendError(
            .missingInstallationIdentity,
            transport: missingIdentityTransport
        )
        XCTAssertTrue(missingIdentityAPI.requests.isEmpty)

        let invalidKeyAPI = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        invalidKeyAPI.recipientKey = Data(repeating: 1, count: 31)
        await assertSendError(.invalidDeviceKey, transport: makeTransport(api: invalidKeyAPI))
        XCTAssertTrue(invalidKeyAPI.requests.isEmpty)

        let timeoutAPI = try MessageCommandAPIFake(deviceID: deviceID, outcome: .timeout)
        await assertSendError(.commandTimedOut, transport: makeTransport(api: timeoutAPI))
        XCTAssertEqual(timeoutAPI.statusRequestCount, 60)
    }

    func testSynchronizeDecryptsOnlyTheIntendedInstallationAndPersistsCursor() async throws {
        let api = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        let receivedAt = Date(timeIntervalSince1970: 1_792_138_150)
        let sourceID = String(repeating: "a", count: 64)
        try api.appendIncomingMessage(
            sourceID: sourceID,
            remoteNumber: "13500000000",
            body: "来自 EC20F 的短信",
            receivedAt: receivedAt
        )
        let cursor = MessageCursorStoreFake(sequence: 0)
        let transport = makeTransport(api: api, cursorStore: cursor)

        let deliveries = try await transport.synchronize(lines: [api.line])

        XCTAssertEqual(
            deliveries,
            [
                IncomingMessageDelivery(
                    id: try XCTUnwrap(api.events.first?.id),
                    sourceID: sourceID,
                    lineID: deviceID,
                    remoteNumber: "13500000000",
                    body: "来自 EC20F 的短信",
                    receivedAt: receivedAt
                ),
            ]
        )
        XCTAssertEqual(api.requestedEventCursors, [0])
        XCTAssertEqual(cursor.sequence, 1)

        let secondPage = try await transport.synchronize(lines: [api.line])
        XCTAssertTrue(secondPage.isEmpty)
        XCTAssertEqual(api.requestedEventCursors, [0, 1])
    }

    func testSynchronizeIgnoresWrongInstallationTamperedAndUnrelatedEvents() async throws {
        let api = try MessageCommandAPIFake(deviceID: deviceID, outcome: .success)
        try api.appendIncomingMessage(
            sourceID: String(repeating: "b", count: 64),
            remoteNumber: "10010",
            body: "valid but for another installation",
            receivedAt: Date(timeIntervalSince1970: 1_792_138_151)
        )
        api.tamperLastEventSignature()
        api.appendUnrelatedEvent()
        let otherIdentity = InstallationIdentity.generate()
        let cursor = MessageCursorStoreFake(sequence: 0)
        let transport = LiveMessageTransport(
            api: api,
            commandSigner: InstallationCommandSigner(identity: otherIdentity),
            cursorStore: cursor,
            waitBeforePollingAgain: {}
        )

        let deliveries = try await transport.synchronize(lines: [api.line])

        XCTAssertTrue(deliveries.isEmpty)
        XCTAssertEqual(cursor.sequence, 2)
    }

    func testSameRemoteNumberOnTwoDevicesRemainsTwoDeliveries() async throws {
        let secondDeviceID = UUID(uuidString: "01900000-0000-7000-8000-000000000203")!
        let installationIdentity = InstallationIdentity.generate()
        let firstAPI = try MessageCommandAPIFake(
            deviceID: deviceID,
            outcome: .success,
            installationIdentity: installationIdentity
        )
        let secondAPI = try MessageCommandAPIFake(
            deviceID: secondDeviceID,
            outcome: .success,
            installationIdentity: installationIdentity
        )
        try firstAPI.appendIncomingMessage(
            sourceID: String(repeating: "c", count: 64),
            remoteNumber: "13500000000",
            body: "SIM 1",
            receivedAt: Date(timeIntervalSince1970: 1_792_138_152)
        )
        try secondAPI.appendIncomingMessage(
            sourceID: String(repeating: "d", count: 64),
            remoteNumber: "13500000000",
            body: "SIM 2",
            receivedAt: Date(timeIntervalSince1970: 1_792_138_153)
        )
        firstAPI.events.append(contentsOf: secondAPI.events.map { secondAPI.replacingSequence($0, with: 2) })
        firstAPI.verificationKeys[secondDeviceID] = secondAPI.deviceVerificationKeyData
        let transport = LiveMessageTransport(
            api: firstAPI,
            commandSigner: InstallationCommandSigner(identity: installationIdentity),
            cursorStore: MessageCursorStoreFake(sequence: 0),
            waitBeforePollingAgain: {}
        )

        let deliveries = try await transport.synchronize(lines: [firstAPI.line, secondAPI.line])

        XCTAssertEqual(deliveries.map(\.lineID), [deviceID, secondDeviceID])
        XCTAssertEqual(Set(deliveries.map(\.remoteNumber)), ["13500000000"])
        XCTAssertEqual(Set(deliveries.map(\.body)), ["SIM 1", "SIM 2"])
    }

    private func makeTransport(
        api: MessageCommandAPIFake,
        cursorStore: any MessageCursorStoring = MessageCursorStoreFake(sequence: 0)
    ) -> LiveMessageTransport {
        LiveMessageTransport(
            api: api,
            commandSigner: InstallationCommandSigner(identity: api.installationIdentity),
            cursorStore: cursorStore,
            makeCommandID: { self.messageID },
            waitBeforePollingAgain: {}
        )
    }

    private func assertSendError(
        _ expectedError: LiveMessageTransportError,
        transport: LiveMessageTransport
    ) async {
        do {
            try await transport.send(
                messageID: messageID,
                lineID: deviceID,
                remoteNumber: "10086",
                body: "test"
            )
            XCTFail("Expected send to fail")
        } catch {
            XCTAssertEqual(error as? LiveMessageTransportError, expectedError)
        }
    }
}

@MainActor
private final class MessageCursorStoreFake: MessageCursorStoring {
    var sequence: Int64

    init(sequence: Int64) {
        self.sequence = sequence
    }
}

@MainActor
private final class MessageCommandAPIFake: EncryptedCallCommandAPI {
    enum Outcome {
        case success
        case modemRejected
        case tamperedResult
        case timeout
    }

    let installationIdentity: InstallationIdentity
    let line: DeviceLineIdentity
    let installationVerificationKey: Curve25519.Signing.PublicKey
    let deviceVerificationKeyData: Data

    private let deviceID: UUID
    private let devicePrivateKey: Curve25519.KeyAgreement.PrivateKey
    private let deviceSigningKey: Curve25519.Signing.PrivateKey
    private let outcome: Outcome
    private var results: [UUID: EncryptedCallCommandResult] = [:]

    var recipientKey: Data
    var verificationKeys: [UUID: Data]
    var events: [EncryptedEventEnvelope] = []
    private(set) var requests: [EncryptedCallCommandRequest] = []
    private(set) var payloads: [MessageCommandPayload] = []
    private(set) var requestedEventCursors: [Int64] = []
    private(set) var statusRequestCount = 0

    init(
        deviceID: UUID,
        outcome: Outcome,
        installationIdentity: InstallationIdentity = .generate()
    ) throws {
        self.deviceID = deviceID
        self.outcome = outcome
        self.installationIdentity = installationIdentity
        devicePrivateKey = Curve25519.KeyAgreement.PrivateKey()
        deviceSigningKey = Curve25519.Signing.PrivateKey()
        recipientKey = devicePrivateKey.publicKey.rawRepresentation
        deviceVerificationKeyData = deviceSigningKey.publicKey.rawRepresentation
        verificationKeys = [deviceID: deviceVerificationKeyData]
        installationVerificationKey = try Curve25519.Signing.PrivateKey(
            rawRepresentation: installationIdentity.signingPrivateKey
        ).publicKey
        line = DeviceLineIdentity(
            id: deviceID,
            numericID: deviceID.uuidString.hasSuffix("203") ? 2 : 1,
            deviceName: "EC20F",
            lineName: "Line",
            phoneNumber: "13500000000",
            isOnline: true,
            commandPublicKey: devicePrivateKey.publicKey.rawRepresentation
        )
    }

    func commandRecipient(deviceID: UUID) async throws -> Data {
        XCTAssertEqual(deviceID, self.deviceID)
        return recipientKey
    }

    func eventVerificationKey(deviceID: UUID) async throws -> Data {
        try XCTUnwrap(verificationKeys[deviceID])
    }

    func enqueueCommand(deviceID: UUID, command: EncryptedCallCommandRequest) async throws {
        XCTAssertEqual(deviceID, self.deviceID)
        XCTAssertTrue(installationVerificationKey.isValidSignature(
            command.signature,
            for: EncryptedCallCommandCodec.signedData(for: command, deviceID: deviceID)
        ))
        requests.append(command)

        let senderKey = try Curve25519.KeyAgreement.PublicKey(
            rawRepresentation: command.senderPublicKey
        )
        let secret = try devicePrivateKey.sharedSecretFromKeyAgreement(with: senderKey)
        let plaintext = try AES.GCM.open(
            try sealedBox(nonce: command.nonce, ciphertext: command.ciphertext),
            using: key(
                secret: secret,
                id: command.id,
                deviceID: deviceID,
                context: "sim-deck-command-v1"
            ),
            authenticating: associatedData(
                context: "sim-deck-command-v1",
                id: command.id,
                deviceID: deviceID
            )
        )
        payloads.append(try JSONDecoder().decode(MessageCommandPayload.self, from: plaintext))

        let result = CallCommandResult(
            version: 1,
            success: outcome != .modemRejected,
            errorCode: outcome == .modemRejected ? "modem_rejected" : nil,
            callState: nil
        )
        let nonce = try AES.GCM.Nonce(data: Data(repeating: 7, count: 12))
        let resultBox = try AES.GCM.seal(
            JSONEncoder().encode(result),
            using: key(
                secret: secret,
                id: command.id,
                deviceID: deviceID,
                context: "sim-deck-command-result-v1"
            ),
            nonce: nonce,
            authenticating: associatedData(
                context: "sim-deck-command-result-v1",
                id: command.id,
                deviceID: deviceID
            )
        )
        var ciphertext = resultBox.ciphertext + resultBox.tag
        if outcome == .tamperedResult {
            ciphertext[ciphertext.startIndex] ^= 0xff
        }
        results[command.id] = EncryptedCallCommandResult(
            nonce: nonce.withUnsafeBytes { Data($0) },
            ciphertext: ciphertext
        )
    }

    func commandStatus(deviceID: UUID, commandID: UUID) async throws -> EncryptedCallCommandStatus {
        statusRequestCount += 1
        return EncryptedCallCommandStatus(
            id: commandID,
            deviceUUID: deviceID,
            state: outcome == .timeout ? "pending" : "completed",
            result: results[commandID]
        )
    }

    func encryptedEvents(afterSequence: Int64) async throws -> EncryptedEventPage {
        requestedEventCursors.append(afterSequence)
        let pageEvents = events.filter { $0.sequence > afterSequence }
        return EncryptedEventPage(
            events: pageEvents,
            nextSequence: events.last?.sequence ?? afterSequence
        )
    }

    func appendIncomingMessage(
        sourceID: String,
        remoteNumber: String,
        body: String,
        receivedAt: Date
    ) throws {
        let eventID = UUID()
        let sequence = Int64(events.count + 1)
        let payload = IncomingMessagePayload(
            version: 1,
            messageID: sourceID,
            deviceUUID: deviceID,
            slotID: line.slotID,
            simID: line.simID,
            numericID: line.numericID,
            remoteNumber: remoteNumber,
            body: body,
            receivedAt: receivedAt,
            segments: 1,
            installationID: installationIdentity.id
        )
        var envelope = EncryptedEventEnvelope(
            id: eventID,
            sequence: sequence,
            type: "message.received",
            deviceUUID: deviceID,
            occurredAt: receivedAt,
            keyEpoch: 1,
            nonce: Data(repeating: UInt8(sequence), count: 12),
            ciphertext: Data(),
            signature: Data()
        )
        let installationPublicKey = try Curve25519.KeyAgreement.PrivateKey(
            rawRepresentation: installationIdentity.keyAgreementPrivateKey
        ).publicKey
        let secret = try devicePrivateKey.sharedSecretFromKeyAgreement(with: installationPublicKey)
        let eventKey = secret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(eventID.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data("sim-deck-message-event-v1".utf8),
            outputByteCount: 32
        )
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let box = try AES.GCM.seal(
            encoder.encode(payload),
            using: eventKey,
            nonce: AES.GCM.Nonce(data: envelope.nonce),
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
            ciphertext: box.ciphertext + box.tag,
            signature: Data()
        )
        let signature = try deviceSigningKey.signature(
            for: EncryptedCallEventCodec.signedData(envelope)
        )
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

    func tamperLastEventSignature() {
        guard let last = events.popLast() else { return }
        var signature = last.signature
        signature[signature.startIndex] ^= 0xff
        events.append(EncryptedEventEnvelope(
            id: last.id,
            sequence: last.sequence,
            type: last.type,
            deviceUUID: last.deviceUUID,
            occurredAt: last.occurredAt,
            keyEpoch: last.keyEpoch,
            nonce: last.nonce,
            ciphertext: last.ciphertext,
            signature: signature
        ))
    }

    func appendUnrelatedEvent() {
        events.append(EncryptedEventEnvelope(
            id: UUID(),
            sequence: Int64(events.count + 1),
            type: "call.state",
            deviceUUID: deviceID,
            occurredAt: Date(timeIntervalSince1970: 1_792_138_152),
            keyEpoch: 1,
            nonce: Data(repeating: 0, count: 12),
            ciphertext: Data(repeating: 0, count: 17),
            signature: Data(repeating: 0, count: 64)
        ))
    }

    func replacingSequence(_ event: EncryptedEventEnvelope, with sequence: Int64) -> EncryptedEventEnvelope {
        EncryptedEventEnvelope(
            id: event.id,
            sequence: sequence,
            type: event.type,
            deviceUUID: event.deviceUUID,
            occurredAt: event.occurredAt,
            keyEpoch: event.keyEpoch,
            nonce: event.nonce,
            ciphertext: event.ciphertext,
            signature: event.signature
        )
    }

    private func key(
        secret: SharedSecret,
        id: UUID,
        deviceID: UUID,
        context: String
    ) -> SymmetricKey {
        secret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: Data("\(id.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8),
            sharedInfo: Data(context.utf8),
            outputByteCount: 32
        )
    }

    private func associatedData(context: String, id: UUID, deviceID: UUID) -> Data {
        Data("\(context)\0\(id.uuidString.lowercased())\0\(deviceID.uuidString.lowercased())".utf8)
    }

    private func sealedBox(nonce: Data, ciphertext: Data) throws -> AES.GCM.SealedBox {
        try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: nonce),
            ciphertext: Data(ciphertext.dropLast(16)),
            tag: Data(ciphertext.suffix(16))
        )
    }
}
