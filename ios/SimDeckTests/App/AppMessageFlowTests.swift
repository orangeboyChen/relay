// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class AppMessageFlowTests: XCTestCase {
    private let firstLine = DeviceLineIdentity(
        id: UUID(uuidString: "01900000-0000-7000-8000-000000000301")!,
        numericID: 1,
        deviceName: "EC20F 1",
        lineName: "Primary",
        phoneNumber: "13500000000",
        isOnline: true
    )
    private let secondLine = DeviceLineIdentity(
        id: UUID(uuidString: "01900000-0000-7000-8000-000000000302")!,
        numericID: 2,
        deviceName: "EC20F 2",
        lineName: "Secondary",
        phoneNumber: "13000000002",
        isOnline: true
    )

    func testSendPublishesSendingThenSentAndUsesNormalizedRecipient() async throws {
        let transport = MessageTransportFake(sendMode: .suspended)
        let model = makeModel(transport: transport)

        XCTAssertEqual(transport.routedLineIDs, [firstLine.id, secondLine.id])

        let sendTask = Task {
            try await model.sendMessage(
                lineID: firstLine.id,
                remoteNumber: "+86 135 0000 0000",
                body: "hello"
            )
        }
        await transport.waitUntilSendStarted()

        let pending = try XCTUnwrap(model.messageItems.first)
        XCTAssertEqual(pending.status, .sending)
        XCTAssertEqual(pending.body, "hello")
        XCTAssertEqual(model.conversations.first?.remoteNumber, "+8613500000000")
        XCTAssertEqual(
            model.conversations.first?.lineSnapshot,
            LineHistorySnapshot(line: firstLine)
        )

        transport.finishSuspendedSend()
        let messageID = try await sendTask.value

        XCTAssertEqual(model.messageItems.first?.id, messageID)
        XCTAssertEqual(model.messageItems.first?.status, .sent)
        XCTAssertEqual(transport.sent.first?.lineID, firstLine.id)
        XCTAssertEqual(transport.sent.first?.remoteNumber, "+8613500000000")
        XCTAssertEqual(transport.sent.first?.body, "hello")
    }

    func testSendFailureKeepsDraftContentAsFailedMessageAndRetryUsesSameID() async throws {
        let transport = MessageTransportFake(sendMode: .failure)
        let model = makeModel(transport: transport)

        do {
            try await model.sendMessage(
                lineID: firstLine.id,
                remoteNumber: "10086",
                body: "please retry"
            )
            XCTFail("Expected the transport error")
        } catch {
            XCTAssertEqual(error as? MessageTransportFake.Error, .sendFailed)
        }

        let failed = try XCTUnwrap(model.messageItems.first)
        XCTAssertEqual(failed.status, .failed)
        XCTAssertEqual(failed.body, "please retry")
        transport.sendMode = .success

        try await model.retryMessage(id: failed.id)

        XCTAssertEqual(model.messageItems.first?.status, .sent)
        XCTAssertEqual(transport.sent.map(\.messageID), [failed.id, failed.id])
    }

    func testDeletingMessagesPersistsEncryptedHistoryAndRemovesEmptyConversation() throws {
        let directory = FileManager.default.temporaryDirectory
            .appending(path: UUID().uuidString, directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let historyStore = EncryptedMessageHistoryStore(
            fileURL: directory.appending(path: "messages.bin")
        )
        let vaultKey = try VaultKey(data: Data(repeating: 9, count: VaultKey.length))
        let firstMessage = MessageItem(
            id: UUID(),
            direction: .received,
            body: "first",
            attachment: nil,
            sentAt: Date(timeIntervalSince1970: 100),
            status: .delivered
        )
        let secondMessage = MessageItem(
            id: UUID(),
            direction: .sent,
            body: "second",
            attachment: nil,
            sentAt: Date(timeIntervalSince1970: 101),
            status: .sent
        )
        let thirdMessage = MessageItem(
            id: UUID(),
            direction: .received,
            body: "another conversation",
            attachment: nil,
            sentAt: Date(timeIntervalSince1970: 102),
            status: .delivered
        )
        let conversation = MessageConversation(
            lineID: firstLine.id,
            remoteNumber: "10086",
            contactName: nil,
            unreadCount: 1,
            messages: [firstMessage, secondMessage]
        )
        let otherConversation = MessageConversation(
            lineID: secondLine.id,
            remoteNumber: "10010",
            contactName: nil,
            unreadCount: 1,
            messages: [thirdMessage]
        )
        try historyStore.save([conversation, otherConversation], using: vaultKey)
        let defaults = UserDefaults(suiteName: "AppMessageDeleteTests.\(UUID().uuidString)")!
        let connection = ServerConnectionManager(
            network: MessageServerNetwork(),
            sessionStore: MessageSessionStore(value: Self.activeSession),
            defaults: defaults
        )
        let model = AppModel(
            devices: [firstLine],
            serverConnection: connection,
            installationBootstrapper: MessageInstallationBootstrapper(vaultKey: vaultKey),
            messageTransport: MessageTransportFake(),
            messageHistoryStore: historyStore,
            remoteDeviceRefreshEnabled: false
        )
        model.bootstrapInstallation()

        try model.deleteMessage(id: firstMessage.id)

        XCTAssertEqual(Set(model.messageItems.map(\.id)), [secondMessage.id, thirdMessage.id])
        XCTAssertEqual(model.conversations.first(where: { $0.id == conversation.id })?.unreadCount, 1)
        XCTAssertEqual(
            try historyStore.load(using: vaultKey).first(where: { $0.id == conversation.id })?.messages,
            [secondMessage]
        )

        try model.deleteMessages(ids: [secondMessage.id, thirdMessage.id])

        XCTAssertTrue(model.conversations.isEmpty)
        XCTAssertTrue(try historyStore.load(using: vaultKey).isEmpty)
    }

    func testRefreshDeduplicatesIncomingMessagesAndSeparatesSIMConversations() async {
        let sourceID = String(repeating: "e", count: 64)
        let transport = MessageTransportFake(
            deliveries: [
                IncomingMessageDelivery(
                    id: UUID(),
                    sourceID: sourceID,
                    lineID: firstLine.id,
                    remoteNumber: "13800138000",
                    body: "first SIM",
                    receivedAt: Date(timeIntervalSince1970: 100)
                ),
                IncomingMessageDelivery(
                    id: UUID(),
                    sourceID: sourceID,
                    lineID: firstLine.id,
                    remoteNumber: "13800138000",
                    body: "duplicate",
                    receivedAt: Date(timeIntervalSince1970: 101)
                ),
                IncomingMessageDelivery(
                    id: UUID(),
                    sourceID: String(repeating: "f", count: 64),
                    lineID: secondLine.id,
                    remoteNumber: "13800138000",
                    body: "second SIM",
                    receivedAt: Date(timeIntervalSince1970: 102)
                ),
            ]
        )
        let model = makeModel(transport: transport)

        await model.refreshMessages()

        XCTAssertEqual(model.conversations.count, 2)
        XCTAssertEqual(Set(model.conversations.map(\.lineID)), [firstLine.id, secondLine.id])
        XCTAssertEqual(model.conversations.flatMap(\.messages).count, 2)
        XCTAssertEqual(model.conversations.map(\.unreadCount), [1, 1])
        XCTAssertEqual(
            Set(model.conversations.compactMap(\.lineSnapshot?.numericID)),
            Set([1, 2])
        )
        XCTAssertEqual(transport.synchronizedLineIDs, [[firstLine.id, secondLine.id]])
    }

    func testRefreshResolvesFormattedContactNameForEverySIMConversation() async {
        let contactStore = MessageContactStore(
            authorizationState: .authorized,
            contacts: [
                ContactRecord(
                    id: "zhang",
                    givenName: "三",
                    familyName: "张",
                    organizationName: "",
                    phoneNumbers: [
                        ContactPhoneNumber(
                            id: "zhang:0",
                            label: "mobile",
                            value: "+86 (138) 0013-8000"
                        ),
                    ],
                    thumbnailImageData: nil
                ),
            ]
        )
        let transport = MessageTransportFake(
            deliveries: [
                IncomingMessageDelivery(
                    id: UUID(),
                    sourceID: String(repeating: "a", count: 64),
                    lineID: firstLine.id,
                    remoteNumber: "13800138000",
                    body: "first SIM",
                    receivedAt: Date(timeIntervalSince1970: 100)
                ),
                IncomingMessageDelivery(
                    id: UUID(),
                    sourceID: String(repeating: "b", count: 64),
                    lineID: secondLine.id,
                    remoteNumber: "+86 138 0013 8000",
                    body: "second SIM",
                    receivedAt: Date(timeIntervalSince1970: 101)
                ),
            ]
        )
        let model = makeModel(transport: transport, contactStore: contactStore)

        model.refreshContactNames()
        await model.refreshMessages()

        XCTAssertEqual(model.conversations.map(\.contactName), ["张三", "张三"])
        XCTAssertEqual(Set(model.conversations.map(\.lineID)), [firstLine.id, secondLine.id])
    }

    func testRevokedContactAccessRemovesPreviouslyResolvedName() {
        let conversation = MessageConversation(
            lineID: firstLine.id,
            remoteNumber: "13800138000",
            contactName: "张三",
            unreadCount: 0,
            messages: []
        )
        let contactStore = MessageContactStore(
            authorizationState: .denied,
            contacts: []
        )
        let model = makeModel(
            transport: MessageTransportFake(),
            messages: .loaded([conversation]),
            contactStore: contactStore
        )

        model.refreshContactNames()

        XCTAssertNil(model.conversations.first?.contactName)
    }

    func testConversationAlternativesIncludeTheSameRecipientAcrossSIMs() {
        let first = MessageConversation(
            lineID: firstLine.id,
            remoteNumber: "+86 135 0000 0000",
            contactName: "Alex",
            unreadCount: 0,
            messages: []
        )
        let second = MessageConversation(
            lineID: secondLine.id,
            remoteNumber: "+8613500000000",
            contactName: "Alex",
            unreadCount: 0,
            messages: []
        )
        let unrelated = MessageConversation(
            lineID: secondLine.id,
            remoteNumber: "13800000000",
            contactName: nil,
            unreadCount: 0,
            messages: []
        )

        XCTAssertEqual(
            first.lineAlternatives(in: [first, unrelated, second]).map(\.id),
            [first.id, second.id]
        )
        XCTAssertNotEqual(first.id, second.id)
    }

    func testHistoricalConversationKeepsItsSIMIdentityAfterLineRemoval() throws {
        let conversation = MessageConversation(
            lineID: firstLine.id,
            remoteNumber: "10086",
            contactName: nil,
            unreadCount: 0,
            messages: []
        )
        let model = makeModel(
            transport: MessageTransportFake(),
            devices: [firstLine],
            messages: .loaded([conversation])
        )

        model.devices = []

        let retained = try XCTUnwrap(model.conversations.first)
        XCTAssertEqual(retained.lineSnapshot, LineHistorySnapshot(line: firstLine))
        XCTAssertEqual(retained.linePresentation(in: []).snapshot?.numericID, 1)
        XCTAssertFalse(retained.linePresentation(in: []).isAvailable)
    }

    func testConversationReadStateCanBeToggledFromListActions() throws {
        let conversation = MessageConversation(
            lineID: firstLine.id,
            remoteNumber: "10086",
            contactName: nil,
            unreadCount: 2,
            messages: []
        )
        let model = makeModel(
            transport: MessageTransportFake(),
            messages: .loaded([conversation])
        )

        model.markConversationRead(conversation.id)

        XCTAssertEqual(try XCTUnwrap(model.conversations.first).unreadCount, 0)

        model.markConversationUnread(conversation.id)
        model.markConversationUnread(conversation.id)

        XCTAssertEqual(try XCTUnwrap(model.conversations.first).unreadCount, 1)
    }

    func testRetryRejectsRemovedLineBeforeTransport() async throws {
        let failedMessage = MessageItem(
            id: UUID(),
            direction: .sent,
            body: "retry",
            attachment: nil,
            sentAt: Date(timeIntervalSince1970: 100),
            status: .failed
        )
        let conversation = MessageConversation(
            lineID: firstLine.id,
            lineSnapshot: LineHistorySnapshot(line: firstLine),
            remoteNumber: "10086",
            contactName: nil,
            unreadCount: 0,
            messages: [failedMessage]
        )
        let transport = MessageTransportFake()
        let model = makeModel(
            transport: transport,
            devices: [],
            messages: .loaded([conversation])
        )

        do {
            try await model.retryMessage(id: failedMessage.id)
            XCTFail("Expected removed line rejection")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .deviceUnavailable)
        }

        XCTAssertTrue(transport.sent.isEmpty)
        XCTAssertEqual(model.messageItems.first?.status, .failed)
    }

    func testForegroundRefreshSynchronizesMessagesWhenPushIsUnavailable() async {
        let transport = MessageTransportFake()
        let model = makeModel(transport: transport, remoteDeviceRefreshEnabled: false)

        await model.refreshForegroundState()

        XCTAssertEqual(transport.synchronizedLineIDs, [[firstLine.id, secondLine.id]])
    }

    func testSendRejectsOfflineLineBeforeTransport() async {
        var offlineLine = firstLine
        offlineLine.isOnline = false
        let transport = MessageTransportFake()
        let model = makeModel(transport: transport, devices: [offlineLine])

        do {
            try await model.sendMessage(
                lineID: offlineLine.id,
                remoteNumber: "10086",
                body: "hello"
            )
            XCTFail("Expected offline line rejection")
        } catch {
            XCTAssertEqual(error as? ServerConnectionError, .deviceUnavailable)
        }

        XCTAssertTrue(transport.sent.isEmpty)
        XCTAssertTrue(model.messageItems.isEmpty)
    }

    private func makeModel(
        transport: MessageTransportFake,
        devices: [DeviceLineIdentity]? = nil,
        messages: MessageListState = .loaded([]),
        contactStore: (any ContactStoreProviding)? = nil,
        remoteDeviceRefreshEnabled: Bool = true
    ) -> AppModel {
        let defaults = UserDefaults(suiteName: "AppMessageFlowTests.\(UUID().uuidString)")!
        defaults.set("https://deck.example.test", forKey: "sim-deck.server-address.v1")
        let connection = ServerConnectionManager(
            network: MessageServerNetwork(),
            sessionStore: MessageSessionStore(value: Self.activeSession),
            defaults: defaults
        )
        return AppModel(
            devices: devices ?? [firstLine, secondLine],
            messages: messages,
            serverConnection: connection,
            messageTransport: transport,
            contactStore: contactStore,
            remoteDeviceRefreshEnabled: remoteDeviceRefreshEnabled
        )
    }

    private static let activeSession = Data("""
    {"access_token":"access","refresh_token":"refresh","access_expires_at":"2098-01-01T00:00:00Z","refresh_expires_at":"2099-01-01T00:00:00Z"}
    """.utf8)
}

@MainActor
private final class MessageContactStore: ContactStoreProviding {
    var authorizationState: ContactAuthorizationState
    var contacts: [ContactRecord]

    init(authorizationState: ContactAuthorizationState, contacts: [ContactRecord]) {
        self.authorizationState = authorizationState
        self.contacts = contacts
    }

    func requestAccess() async throws -> Bool {
        authorizationState == .authorized || authorizationState == .limited
    }

    func fetchContacts() throws -> [ContactRecord] {
        contacts
    }

    func changes() -> AsyncStream<Void> {
        AsyncStream { $0.finish() }
    }
}

private extension AppModel {
    var conversations: [MessageConversation] {
        guard case .loaded(let conversations) = messages else { return [] }
        return conversations
    }

    var messageItems: [MessageItem] {
        conversations.flatMap(\.messages)
    }
}

private struct MessageInstallationBootstrapper: InstallationBootstrapping {
    let vaultKey: VaultKey

    func bootstrap() throws -> InstallationBootstrapResult {
        InstallationBootstrapResult(
            identity: InstallationIdentity(
                id: UUID(uuidString: "01900000-0000-7000-8000-000000000901")!,
                signingPrivateKey: Data(repeating: 1, count: 32),
                keyAgreementPrivateKey: Data(repeating: 2, count: 32),
                pendingRevocationIDs: []
            ),
            recoveredVaultKey: vaultKey,
            pendingRevocationIDs: []
        )
    }

    func saveVaultKey(_ key: VaultKey) throws {}
    func markInstallationRevoked(_ id: UUID) throws {}
}

@MainActor
private final class MessageTransportFake: MessageTransport {
    enum Error: Swift.Error, Equatable {
        case sendFailed
    }

    enum SendMode {
        case success
        case failure
        case suspended
    }

    struct SentMessage: Equatable {
        let messageID: UUID
        let lineID: UUID
        let remoteNumber: String
        let body: String
    }

    var sendMode: SendMode
    var deliveries: [IncomingMessageDelivery]
    private(set) var sent: [SentMessage] = []
    private(set) var routedLineIDs: [UUID] = []
    private(set) var synchronizedLineIDs: [[UUID]] = []
    private var sendStartedContinuations: [CheckedContinuation<Void, Never>] = []
    private var sendCompletion: CheckedContinuation<Void, Never>?
    private var didStartSend = false

    init(sendMode: SendMode = .success, deliveries: [IncomingMessageDelivery] = []) {
        self.sendMode = sendMode
        self.deliveries = deliveries
    }

    func send(messageID: UUID, lineID: UUID, remoteNumber: String, body: String) async throws {
        sent.append(
            SentMessage(
                messageID: messageID,
                lineID: lineID,
                remoteNumber: remoteNumber,
                body: body
            )
        )
        didStartSend = true
        sendStartedContinuations.forEach { $0.resume() }
        sendStartedContinuations.removeAll()

        switch sendMode {
        case .success:
            return
        case .failure:
            throw Error.sendFailed
        case .suspended:
            await withCheckedContinuation { continuation in
                sendCompletion = continuation
            }
        }
    }

    func synchronize(lines: [DeviceLineIdentity]) async throws -> MessageSyncBatch {
        synchronizedLineIDs.append(lines.map(\.id))
        return MessageSyncBatch(deliveries: deliveries, nextSequence: 7)
    }

    func updateLineRoutes(_ lines: [DeviceLineIdentity]) {
        routedLineIDs = lines.map(\.id)
    }

    func waitUntilSendStarted() async {
        guard !didStartSend else { return }
        await withCheckedContinuation { continuation in
            sendStartedContinuations.append(continuation)
        }
    }

    func finishSuspendedSend() {
        sendCompletion?.resume()
        sendCompletion = nil
    }
}

@MainActor
private struct MessageServerNetwork: ServerNetworking {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        throw URLError(.unsupportedURL)
    }
}

private final class MessageSessionStore: SecureValueStoring, @unchecked Sendable {
    private var value: Data?

    init(value: Data?) {
        self.value = value
    }

    func read() -> Data? { value }
    func write(_ value: Data) { self.value = value }
    func delete() { value = nil }
}
