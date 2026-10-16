#if DEBUG
import Foundation

extension AppModel {
    static func preview(
        selectedTab: AppTab = .calls,
        devices: [DeviceLineIdentity] = PreviewFixtures.devices,
        keyRecoveryState: KeyRecoveryState = .available
    ) -> AppModel {
        let serverConnection = ServerConnectionManager(
            network: PreviewServerNetworking(),
            sessionStore: PreviewSecureValueStore(),
            defaults: UserDefaults(suiteName: "sim-deck.preview")!
        )
        serverConnection.address = "https://sim-deck.example.com"
        return AppModel(
            devices: devices,
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
            installationBootstrapper: PreviewInstallationBootstrapper()
        )
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
#endif
