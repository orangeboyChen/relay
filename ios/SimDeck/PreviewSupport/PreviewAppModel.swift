// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

#if DEBUG
import Foundation

extension AppModel {
    static func preview(
        selectedTab: AppTab = .calls,
        devices: [DeviceLineIdentity] = PreviewFixtures.devices,
        keyRecoveryState: KeyRecoveryState = .available,
        onboardingCompleted: Bool = true,
        activeCall: ActiveCallSession? = nil,
        slotSettings: SlotSettings = .enabled
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
        let callHistoryStore = EncryptedCallHistoryStore(
            fileURL: FileManager.default.temporaryDirectory
                .appending(path: "sim-deck-preview-calls-\(UUID().uuidString).bin")
        )
        if let vaultKey = try? VaultKey(data: Data(repeating: 2, count: VaultKey.length)) {
            try? messageHistoryStore.save(PreviewFixtures.conversations, using: vaultKey)
            try? callHistoryStore.save(PreviewFixtures.calls, using: vaultKey)
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
                integratesWithSystem: false,
                initialCall: activeCall,
                audioRoutes: CallAudioRouteController(source: PreviewCallAudioRouteSource())
            ),
            installationBootstrapper: PreviewInstallationBootstrapper(),
            messageTransport: PreviewMessageTransport(),
            slotSettingsTransport: PreviewSlotSettingsTransport(settings: slotSettings),
            esimProfileManager: PreviewESIMProfileManager(),
            onboardingStore: PreviewOnboardingStore(isCompleted: onboardingCompleted),
            passkeyAuthorizer: PreviewPasskeyAuthorizer(),
            contactStore: PreviewContactStore(contacts: PreviewContactFixtures.contacts),
            messageHistoryStore: messageHistoryStore,
            callHistoryStore: callHistoryStore,
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
    private var settings: SlotSettings

    init(settings: SlotSettings) {
        self.settings = settings
    }

    func load(line: DeviceLineIdentity) async throws -> SlotSettings { settings }

    func update(_ settings: SlotSettings, line: DeviceLineIdentity) async throws -> SlotSettings {
        self.settings = settings
        return settings
    }

    func restartModem(line: DeviceLineIdentity) async throws {}
}

@MainActor
private final class PreviewESIMProfileManager: ESIMProfileManaging {
    private var profiles: [ESIMProfile] = [
        ESIMProfile(
            id: UUID(uuidString: "50000000-0000-0000-0000-000000000001")!,
            profileID: "profile-work",
            name: "Work line",
            iccid: "8986000000000000001",
            operatorName: "China Mobile",
            isEnabled: true
        ),
        ESIMProfile(
            id: UUID(uuidString: "50000000-0000-0000-0000-000000000002")!,
            profileID: "profile-travel",
            iccid: "8986000000000000002",
            operatorName: "Travel eSIM"
        )
    ]

    func loadProfiles(line: DeviceLineIdentity) async throws -> [ESIMProfile] {
        profiles
    }

    func setEnabled(_ enabled: Bool, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        guard let index = profiles.firstIndex(where: { $0.id == profile.id }) else {
            throw ESIMProfileManagerError.unavailable
        }
        if enabled {
            for index in profiles.indices { profiles[index].isEnabled = false }
        }
        profiles[index].isEnabled = enabled
        return profiles[index]
    }

    func rename(_ name: String, profile: ESIMProfile, line: DeviceLineIdentity) async throws -> ESIMProfile {
        guard let index = profiles.firstIndex(where: { $0.id == profile.id }) else {
            throw ESIMProfileManagerError.unavailable
        }
        profiles[index].name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return profiles[index]
    }

    func delete(profile: ESIMProfile, line: DeviceLineIdentity) async throws {
        profiles.removeAll { $0.id == profile.id }
    }

    func addActivationCode(_ code: String, line: DeviceLineIdentity) async throws -> ESIMProfile {
        let trimmedCode = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedCode.isEmpty else { throw ESIMProfileManagerError.invalidActivationCode }
        let profile = ESIMProfile(
            profileID: trimmedCode,
            operatorName: "Pending activation"
        )
        profiles.append(profile)
        return profile
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
