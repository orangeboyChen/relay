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
        self.isOnboardingPresented = !resolvedOnboardingStore.isCompleted
        self.keyRecoveryState = keyRecoveryState
        self.installationTrust = nil
        self.pendingInstallationRevocationIDs = []
        self.serverConnection = resolvedServerConnection
        self.callCoordinator = callCoordinator ?? CallKitCoordinator(
            transport: LiveCallTransport(api: resolvedServerConnection),
            incomingCallClaimer: resolvedServerConnection,
            recentCallLineStore: resolvedRecentCallLineStore
        )
        self.installationBootstrapper = installationBootstrapper
        self.defaultLineStore = resolvedDefaultLineStore
        self.recentCallLineStore = resolvedRecentCallLineStore
        self.onboardingStore = resolvedOnboardingStore
    }

    func completeOnboarding() {
        onboardingStore.markCompleted()
        isOnboardingPresented = false
    }

    func bootstrapInstallation() {
        do {
            let result = try installationBootstrapper.bootstrap()
            installationIdentity = result.identity
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
            try? await voipPushCoordinator?.uploadPendingToken()
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
        try? await voipPushCoordinator?.uploadPendingToken()
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

    func createDevicePairingToken() async throws -> DevicePairingToken {
        try await serverConnection.createDevicePairingToken()
    }

    func saveDevice(
        id: UUID,
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
        try await serverConnection.updateDeviceProfile(
            deviceID: id,
            profileCiphertext: ciphertext
        )
        try await serverConnection.updateDeviceNumericID(deviceID: id, numericID: numericID)
        devices = try await serverConnection.loadDevices(profileKey: recoveredVaultKey)
    }

    func removeDevice(id: UUID) async throws {
        try await serverConnection.revokeDevice(deviceID: id)
        devices.removeAll { $0.id == id }
        if defaultCallLineID == id {
            defaultCallLineID = nil
        }
        if defaultMessageLineID == id {
            defaultMessageLineID = nil
        }
        if lastSuccessfulDialLineID == id {
            lastSuccessfulDialLineID = nil
        }
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
                lineID: opened.payload.deviceUUID,
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
