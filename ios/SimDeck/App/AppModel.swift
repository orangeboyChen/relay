import Foundation
import Observation

@MainActor
@Observable
final class AppModel {
    var devices: [DeviceLineIdentity]
    var callHistory: CallHistoryState
    var messages: MessageListState
    var lastSuccessfulDialLineID: UUID?
    var defaultCallLineID: UUID? {
        didSet {
            guard defaultCallLineID != oldValue else { return }
            defaultLineStore.callLineID = defaultCallLineID
        }
    }
    var defaultMessageLineID: UUID? {
        didSet {
            guard defaultMessageLineID != oldValue else { return }
            defaultLineStore.messageLineID = defaultMessageLineID
        }
    }
    var pendingExternalDialRequest: ExternalDialRequest?
    var selectedTab: AppTab
    private(set) var isOnboardingPresented: Bool
    var keyRecoveryState: KeyRecoveryState
    var installationID: UUID?
    private(set) var installationTrust: AppInstallationTrust?
    var pendingInstallationRevocationIDs: [UUID]
    let serverConnection: ServerConnectionManager
    let callCoordinator: CallKitCoordinator
    let installationBootstrapper: any InstallationBootstrapping
    @ObservationIgnored private let defaultLineStore: any DefaultLineStoring
    @ObservationIgnored private let recentCallLineStore: any RecentCallLineStoring
    @ObservationIgnored private let onboardingStore: any OnboardingStoring
    @ObservationIgnored private var installationIdentity: InstallationIdentity?
    @ObservationIgnored private var recoveredVaultKey: VaultKey?
    @ObservationIgnored private var voipPushCoordinator: VoIPPushCoordinator?
    @ObservationIgnored private var alertPushCoordinator: AlertPushCoordinator?
    @ObservationIgnored private let commandSigner: InstallationCommandSigner
    @ObservationIgnored private let messageTransport: any MessageTransport

    var canEditDeviceProfiles: Bool { recoveredVaultKey != nil }

    init(
        devices: [DeviceLineIdentity] = [],
        callHistory: CallHistoryState = .loaded([]),
        messages: MessageListState = .loaded([]),
        lastSuccessfulDialLineID: UUID? = nil,
        defaultCallLineID: UUID? = nil,
        defaultMessageLineID: UUID? = nil,
        selectedTab: AppTab = .calls,
        keyRecoveryState: KeyRecoveryState = .checking,
        serverConnection: ServerConnectionManager? = nil,
        callCoordinator: CallKitCoordinator? = nil,
        installationBootstrapper: any InstallationBootstrapping = InstallationBootstrapper.live(),
        messageTransport: (any MessageTransport)? = nil,
        defaultLineStore: (any DefaultLineStoring)? = nil,
        recentCallLineStore: (any RecentCallLineStoring)? = nil,
        onboardingStore: (any OnboardingStoring)? = nil
    ) {
        let resolvedServerConnection = serverConnection ?? .live()
        let resolvedDefaultLineStore = defaultLineStore ?? UserDefaultsDefaultLineStore()
        let resolvedRecentCallLineStore = recentCallLineStore ?? UserDefaultsRecentCallLineStore()
        self.devices = devices
        self.callHistory = callHistory
        self.messages = messages
        self.lastSuccessfulDialLineID = lastSuccessfulDialLineID
        self.defaultCallLineID = defaultCallLineID ?? resolvedDefaultLineStore.callLineID
        self.defaultMessageLineID = defaultMessageLineID ?? resolvedDefaultLineStore.messageLineID
        self.pendingExternalDialRequest = nil
        self.selectedTab = selectedTab
        let resolvedOnboardingStore = onboardingStore ?? UserDefaultsOnboardingStore()
        let resolvedCommandSigner = InstallationCommandSigner()
        self.isOnboardingPresented = !resolvedOnboardingStore.isCompleted
        self.keyRecoveryState = keyRecoveryState
        self.installationTrust = nil
        self.pendingInstallationRevocationIDs = []
        self.serverConnection = resolvedServerConnection
        self.callCoordinator = callCoordinator ?? CallKitCoordinator(
            transport: LiveCallTransport(
                api: resolvedServerConnection,
                commandSigner: resolvedCommandSigner
            ),
            incomingCallClaimer: resolvedServerConnection,
            recentCallLineStore: resolvedRecentCallLineStore
        )
        self.installationBootstrapper = installationBootstrapper
        self.defaultLineStore = resolvedDefaultLineStore
        self.recentCallLineStore = resolvedRecentCallLineStore
        self.onboardingStore = resolvedOnboardingStore
        self.commandSigner = resolvedCommandSigner
        self.messageTransport = messageTransport ?? LiveMessageTransport(
            api: resolvedServerConnection,
            commandSigner: resolvedCommandSigner,
            cursorStore: UserDefaultsMessageCursorStore {
                resolvedCommandSigner.identity?.id
            }
        )
        self.callCoordinator.updateLineRoutes(devices)
        self.messageTransport.updateLineRoutes(devices)
    }

    func completeOnboarding() {
        onboardingStore.markCompleted()
        isOnboardingPresented = false
    }

    func bootstrapInstallation() {
        do {
            let result = try installationBootstrapper.bootstrap()
            installationIdentity = result.identity
            commandSigner.identity = result.identity
            recoveredVaultKey = result.recoveredVaultKey
            installationID = result.identity.id
            pendingInstallationRevocationIDs = result.pendingRevocationIDs
            keyRecoveryState = result.recoveredVaultKey == nil ? .approvalRequired : .available
        } catch {
            keyRecoveryState = .unavailable
        }
    }

    func refreshDevices() async {
        guard serverConnection.isSignedIn else {
            devices = []
            return
        }
        do {
            try await registerInstallationIfNeeded()
            devices = try await serverConnection.loadDevices(profileKey: recoveredVaultKey)
            callCoordinator.updateLineRoutes(devices)
            messageTransport.updateLineRoutes(devices)
            try? await voipPushCoordinator?.uploadPendingToken()
            try? await alertPushCoordinator?.uploadPendingToken()
        } catch {
            devices = devices.map { device in
                var offlineDevice = device
                offlineDevice.isOnline = false
                return offlineDevice
            }
        }
    }

    func signInToServer(password: String) async throws {
        try await serverConnection.signIn(password: password)
        try await registerInstallationIfNeeded()
        devices = try await serverConnection.loadDevices(profileKey: recoveredVaultKey)
        callCoordinator.updateLineRoutes(devices)
        messageTransport.updateLineRoutes(devices)
        try? await voipPushCoordinator?.uploadPendingToken()
        try? await alertPushCoordinator?.uploadPendingToken()
    }

    func registerPasskey() async throws {
        guard let serverURL = serverConnection.authenticatedBaseURL,
              let accessToken = serverConnection.accessToken else {
            throw ServerConnectionError.authenticationRequired
        }
        let callback = try await PasskeyWebAuthenticationSession().authenticate(
            serverURL: serverURL, mode: "register", accessToken: accessToken
        )
        guard callback.scheme == "simdeck", callback.fragment != nil else {
            throw ServerConnectionError.invalidResponse
        }
    }

    func startVoIPPushes() {
        guard let installationIdentity else { return }
        if voipPushCoordinator == nil {
            voipPushCoordinator = VoIPPushCoordinator(
                tokenRegistrar: serverConnection,
                environment: Self.pushEnvironment
            ) { [weak self] data in
                try await self?.processIncomingCallPush(data)
            }
        }
        voipPushCoordinator?.start(installationID: installationIdentity.id)
        startAlertPushes()
    }

    func startAlertPushes() {
        guard let installationIdentity else { return }
        if alertPushCoordinator == nil {
            alertPushCoordinator = AlertPushCoordinator(
                tokenRegistrar: serverConnection,
                environment: Self.pushEnvironment
            )
        }
        alertPushCoordinator?.start(installationID: installationIdentity.id)
    }

    func handleExternalDial(number rawNumber: String, requiredLineID: UUID? = nil) async {
        let number = DialString.normalized(rawNumber)
        guard DialString.isValid(number) else { return }

        selectedTab = .calls
        let lineSelection = requiredLineID.map(ExternalDialLineSelection.preferred) ?? .automatic
        let request = ExternalDialRequest(number: number, lineSelection: lineSelection)
        pendingExternalDialRequest = nil
        guard callCoordinator.activeCall == nil else {
            pendingExternalDialRequest = request
            return
        }

        let resolution = if let requiredLineID {
            OutgoingCallLineRouter.resolve(lines: devices, requiredLineID: requiredLineID)
        } else {
            OutgoingCallLineRouter.resolve(lines: devices, preferredLineID: defaultCallLineID)
        }

        switch resolution {
        case .ready(let line):
            do {
                try await callCoordinator.startCall(to: number, using: line.id)
                lastSuccessfulDialLineID = line.id
            } catch {
                callCoordinator.dismissCallStartFailure()
                pendingExternalDialRequest = ExternalDialRequest(
                    number: number,
                    lineSelection: .preferred(line.id)
                )
            }
        case .selectionRequired, .unavailable:
            pendingExternalDialRequest = request
        }
    }

    func handleDefaultCallingActivity(_ activity: NSUserActivity) async {
        guard let request = DefaultCallingRequestParser.request(from: activity) else { return }
        await handleDefaultCallingRequest(request)
    }

    func handleDefaultCallingRequest(_ request: DefaultCallingRequest) async {
        let requiredLineID = request.isCallBack ? recentLineID(for: request) : nil
        await handleExternalDial(number: request.number, requiredLineID: requiredLineID)
    }

    func testServerConnection() async throws {
        devices = try await serverConnection.loadDevices(profileKey: recoveredVaultKey)
    }

    func refreshMessages() async {
        guard serverConnection.isSignedIn else { return }
        do {
            let incoming = try await messageTransport.synchronize(lines: devices)
            mergeIncomingMessages(incoming)
        } catch {
            if case .loaded(let conversations) = messages, !conversations.isEmpty {
                return
            }
            messages = .failed
        }
    }

    @discardableResult
    func sendMessage(lineID: UUID, remoteNumber: String, body: String) async throws -> UUID {
        guard devices.contains(where: { $0.id == lineID && $0.isOnline }) else {
            throw ServerConnectionError.deviceUnavailable
        }
        let number = DialString.normalized(remoteNumber)
        guard DialString.isValid(number),
              !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw ServerConnectionError.invalidResponse
        }

        let messageID = UUID()
        upsertOutgoingMessage(
            id: messageID,
            lineID: lineID,
            remoteNumber: number,
            body: body,
            status: .sending
        )
        do {
            try await messageTransport.send(
                messageID: messageID,
                lineID: lineID,
                remoteNumber: number,
                body: body
            )
            updateMessageStatus(id: messageID, status: .sent)
            return messageID
        } catch {
            updateMessageStatus(id: messageID, status: .failed)
            throw error
        }
    }

    func retryMessage(id: UUID) async throws {
        guard case .loaded(let conversations) = messages,
              let conversation = conversations.first(where: { conversation in
                  conversation.messages.contains { $0.id == id }
              }),
              let message = conversation.messages.first(where: { $0.id == id }),
              message.direction == .sent else {
            throw ServerConnectionError.invalidResponse
        }
        updateMessageStatus(id: id, status: .sending)
        do {
            try await messageTransport.send(
                messageID: id,
                lineID: conversation.lineID,
                remoteNumber: conversation.remoteNumber,
                body: message.body
            )
            updateMessageStatus(id: id, status: .sent)
        } catch {
            updateMessageStatus(id: id, status: .failed)
            throw error
        }
    }

    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState {
        try await serverConnection.openDeviceDiscovery()
    }

    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState {
        try await serverConnection.deviceDiscoverySession()
    }

    func closeDeviceDiscovery() async throws {
        try await serverConnection.closeDeviceDiscovery()
    }

    func discoveredDevices() async throws -> [DiscoveredDevice] {
        try await serverConnection.discoveredDevices()
    }

    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice {
        let approved = try await serverConnection.approveDiscoveredDevice(id: id)
        await refreshDevices()
        return approved
    }

    func rejectDiscoveredDevice(id: UUID) async throws {
        try await serverConnection.rejectDiscoveredDevice(id: id)
    }

    func saveDevice(
        line: DeviceLineIdentity,
        numericID: Int,
        deviceName: String,
        lineName: String,
        phoneNumber: String
    ) async throws {
        guard let recoveredVaultKey else {
            throw KeyRecoveryError.invalidVaultKey
        }
        let profile = DeviceProfile(
            deviceName: deviceName.trimmingCharacters(in: .whitespacesAndNewlines),
            lineName: lineName.trimmingCharacters(in: .whitespacesAndNewlines),
            phoneNumber: phoneNumber.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        let ciphertext = try DeviceProfileCodec.seal(profile, using: recoveredVaultKey)
        if let simID = line.simID {
            try await serverConnection.updateSIMProfile(
                simID: simID,
                profileCiphertext: ciphertext
            )
            try await serverConnection.updateSIMNumericID(simID: simID, numericID: numericID)
        } else {
            try await serverConnection.updateDeviceProfile(
                deviceID: line.deviceID,
                profileCiphertext: ciphertext
            )
            try await serverConnection.updateDeviceNumericID(
                deviceID: line.deviceID,
                numericID: numericID
            )
        }
        devices = try await serverConnection.loadDevices(profileKey: recoveredVaultKey)
        callCoordinator.updateLineRoutes(devices)
        messageTransport.updateLineRoutes(devices)
    }

    func removeDevice(line: DeviceLineIdentity) async throws {
        try await serverConnection.revokeDevice(deviceID: line.deviceID)
        let removedLineIDs = Set(
            devices.lazy.filter { $0.deviceID == line.deviceID }.map(\.id)
        )
        devices.removeAll { $0.deviceID == line.deviceID }
        if defaultCallLineID.map(removedLineIDs.contains) == true {
            defaultCallLineID = nil
        }
        if defaultMessageLineID.map(removedLineIDs.contains) == true {
            defaultMessageLineID = nil
        }
        if lastSuccessfulDialLineID.map(removedLineIDs.contains) == true {
            lastSuccessfulDialLineID = nil
        }
        callCoordinator.updateLineRoutes(devices)
        messageTransport.updateLineRoutes(devices)
    }

    func signOutFromServer() throws {
        try serverConnection.signOut()
        devices = []
        installationTrust = nil
    }

    private func registerInstallationIfNeeded() async throws {
        guard installationTrust == nil, let installationIdentity else { return }
        installationTrust = try await serverConnection.registerAppInstallation(
            AppInstallationRegistration(
                id: installationIdentity.id,
                identityPublicKey: try installationIdentity.signingPublicKey(),
                keyAgreementPublicKey: try installationIdentity.keyAgreementPublicKey(),
                wrappedVaultKey: nil
            )
        )
    }

    private func recentLineID(for request: DefaultCallingRequest) -> UUID? {
        let normalizedNumber = DialString.normalized(request.number)
        if let callRecordIdentifier = request.callRecordIdentifier,
           let callRecordID = UUID(uuidString: callRecordIdentifier),
           case .loaded(let records) = callHistory,
           let exactRecord = records.first(where: { $0.id == callRecordID }) {
            return exactRecord.lineID
        }
        if let storedLineID = recentCallLineStore.lineID(
            callRecordIdentifier: request.callRecordIdentifier,
            remoteNumber: request.number
        ) {
            return storedLineID
        }
        guard case .loaded(let records) = callHistory else { return nil }
        return records
            .filter { DialString.normalized($0.remoteNumber) == normalizedNumber }
            .max(by: { $0.startedAt < $1.startedAt })?
            .lineID
    }

    private func mergeIncomingMessages(_ incoming: [IncomingMessageDelivery]) {
        var conversations = loadedConversations
        for delivery in incoming {
            let conversationID = ConversationID(
                deviceUUID: delivery.lineID,
                remoteNumber: delivery.remoteNumber
            )
            if let index = conversations.firstIndex(where: { $0.id == conversationID }) {
                guard !conversations[index].messages.contains(where: {
                    $0.sourceID == delivery.sourceID
                }) else { continue }
                conversations[index].messages.append(
                    MessageItem(
                        id: delivery.id,
                        sourceID: delivery.sourceID,
                        direction: .received,
                        body: delivery.body,
                        attachment: nil,
                        sentAt: delivery.receivedAt,
                        status: .delivered
                    )
                )
                conversations[index].messages.sort { $0.sentAt < $1.sentAt }
                conversations[index].unreadCount += 1
            } else {
                conversations.append(
                    MessageConversation(
                        lineID: delivery.lineID,
                        remoteNumber: delivery.remoteNumber,
                        contactName: nil,
                        unreadCount: 1,
                        messages: [
                            MessageItem(
                                id: delivery.id,
                                sourceID: delivery.sourceID,
                                direction: .received,
                                body: delivery.body,
                                attachment: nil,
                                sentAt: delivery.receivedAt,
                                status: .delivered
                            ),
                        ]
                    )
                )
            }
        }
        messages = .loaded(sortedConversations(conversations))
    }

    private func upsertOutgoingMessage(
        id: UUID,
        lineID: UUID,
        remoteNumber: String,
        body: String,
        status: MessageDeliveryStatus
    ) {
        var conversations = loadedConversations
        let conversationID = ConversationID(deviceUUID: lineID, remoteNumber: remoteNumber)
        let message = MessageItem(
            id: id,
            direction: .sent,
            body: body,
            attachment: nil,
            sentAt: Date(),
            status: status
        )
        if let index = conversations.firstIndex(where: { $0.id == conversationID }) {
            conversations[index].messages.append(message)
        } else {
            conversations.append(
                MessageConversation(
                    lineID: lineID,
                    remoteNumber: remoteNumber,
                    contactName: nil,
                    unreadCount: 0,
                    messages: [message]
                )
            )
        }
        messages = .loaded(sortedConversations(conversations))
    }

    private func updateMessageStatus(id: UUID, status: MessageDeliveryStatus) {
        var conversations = loadedConversations
        for conversationIndex in conversations.indices {
            guard let messageIndex = conversations[conversationIndex].messages.firstIndex(
                where: { $0.id == id }
            ) else { continue }
            conversations[conversationIndex].messages[messageIndex].status = status
            messages = .loaded(sortedConversations(conversations))
            return
        }
    }

    private var loadedConversations: [MessageConversation] {
        guard case .loaded(let conversations) = messages else { return [] }
        return conversations
    }

    private func sortedConversations(
        _ conversations: [MessageConversation]
    ) -> [MessageConversation] {
        conversations.sorted {
            ($0.lastMessage?.sentAt ?? .distantPast) > ($1.lastMessage?.sentAt ?? .distantPast)
        }
    }

    private func processIncomingCallPush(_ data: Data) async throws {
        guard let installationIdentity else {
            throw VoIPPushError.invalidPayload
        }
        let push = try IncomingCallPushDelivery.decode(data)
        guard case .incoming(let delivery) = push else {
            if case .answeredElsewhere(let notificationID) = push {
                callCoordinator.reportIncomingCallAnsweredElsewhere(
                    notificationID: notificationID
                )
            }
            return
        }
        let verificationKey = try await serverConnection.eventVerificationKey(
            deviceID: delivery.deviceUUID
        )
        let opened = try IncomingCallNotificationCodec.open(
            data,
            installationIdentity: installationIdentity,
            deviceVerificationKey: verificationKey
        )
        try await callCoordinator.reportIncomingCall(
            IncomingCallPresentation(
                notificationID: opened.delivery.notificationID,
                callID: opened.payload.callID,
                lineID: devices.first(where: {
                    $0.deviceID == opened.payload.deviceUUID && $0.slotID == opened.payload.slotID
                })?.id ?? opened.payload.deviceUUID,
                installationID: installationIdentity.id,
                numericID: opened.payload.numericID,
                remoteNumber: opened.payload.remoteNumber,
                contactName: nil,
                callSecret: opened.payload.callSecret,
                deviceVerificationKey: verificationKey
            )
        )
    }

    private static var pushEnvironment: PushEnvironment {
#if DEBUG
        .sandbox
#else
        .production
#endif
    }
}

enum KeyRecoveryState: Equatable, Sendable {
    case checking
    case available
    case approvalRequired
    case unavailable
}

enum AppTab: Hashable {
    case calls
    case messages
    case profile
}
