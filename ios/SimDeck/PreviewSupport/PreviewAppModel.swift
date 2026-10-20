#if DEBUG
import Foundation

extension AppModel {
    static func preview(
        selectedTab: AppTab = .calls,
        devices: [DeviceLineIdentity] = PreviewFixtures.devices,
        keyRecoveryState: KeyRecoveryState = .available,
        onboardingCompleted: Bool = true
    ) -> AppModel {
        let serverConnection = ServerConnectionManager(
            network: PreviewServerNetworking(),
            sessionStore: PreviewSecureValueStore(),
            defaults: UserDefaults(suiteName: "sim-deck.preview")!
        )
        serverConnection.address = "https://sim-deck.example.com"
        let messageHistoryStore = EncryptedMessageHistoryStore(
            fileURL: FileManager.default.temporaryDirectory
                .appending(path: "sim-deck-preview-\(UUID().uuidString).bin")
        )
        if let vaultKey = try? VaultKey(data: Data(repeating: 2, count: VaultKey.length)) {
            try? messageHistoryStore.save(PreviewFixtures.conversations, using: vaultKey)
        }
        let model = AppModel(
            devices: devices,
            managedSIMs: PreviewFixtures.managedSIMs,
            callHistory: .loaded(PreviewFixtures.calls),
            messages: .loaded(PreviewFixtures.conversations),
            lastSuccessfulDialLineID: PreviewFixtures.devices[0].id,
            defaultCallLineID: PreviewFixtures.devices[0].id,
            defaultMessageLineID: PreviewFixtures.devices[0].id,
            selectedTab: selectedTab,
            keyRecoveryState: keyRecoveryState,
            serverConnection: serverConnection,
            callCoordinator: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            installationBootstrapper: PreviewInstallationBootstrapper(),
            messageTransport: PreviewMessageTransport(),
            slotSettingsTransport: PreviewSlotSettingsTransport(),
            onboardingStore: PreviewOnboardingStore(isCompleted: onboardingCompleted),
            passkeyAuthorizer: PreviewPasskeyAuthorizer(),
            messageHistoryStore: messageHistoryStore,
            remoteDeviceRefreshEnabled: false
        )
        model.bootstrapInstallation()
        return model
    }
}

@MainActor
private final class PreviewPasskeyAuthorizer: PasskeyAuthorizing {
    func register(_ challenge: PasskeyRegistrationChallenge) async throws
        -> PasskeyRegistrationCredential {
        throw CancellationError()
    }

    func assert(_ challenge: PasskeyAssertionChallenge) async throws
        -> PasskeyAssertionCredential {
        throw CancellationError()
    }
}

@MainActor
private final class PreviewMessageTransport: MessageTransport {
    func send(messageID: UUID, lineID: UUID, remoteNumber: String, body: String) async throws {}

    func synchronize(lines: [DeviceLineIdentity]) async throws -> MessageSyncBatch {
        MessageSyncBatch(deliveries: [], nextSequence: 0)
    }
}

@MainActor
private final class PreviewSlotSettingsTransport: SlotSettingsTransport {
    func load(line: DeviceLineIdentity) async throws -> SlotSettings { .enabled }

    func update(_ settings: SlotSettings, line: DeviceLineIdentity) async throws -> SlotSettings {
        settings
    }
}

@MainActor
private struct PreviewServerNetworking: ServerNetworking {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        throw URLError(.cancelled)
    }
}

private final class PreviewSecureValueStore: SecureValueStoring, @unchecked Sendable {
    func read() -> Data? { nil }
    func write(_ value: Data) {}
    func delete() {}
}

@MainActor
private final class PreviewOnboardingStore: OnboardingStoring {
    private(set) var isCompleted: Bool

    init(isCompleted: Bool) {
        self.isCompleted = isCompleted
    }

    func markCompleted() {
        isCompleted = true
    }
}
#endif
