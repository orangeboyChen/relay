#if DEBUG
extension AppModel {
    static func preview(selectedTab: AppTab = .calls) -> AppModel {
        AppModel(
            devices: PreviewFixtures.devices,
            callHistory: .loaded(PreviewFixtures.calls),
            messages: .loaded(PreviewFixtures.conversations),
            lastSuccessfulDialLineID: PreviewFixtures.devices[0].id,
            selectedTab: selectedTab,
            keyRecoveryState: .available,
            callCoordinator: CallKitCoordinator(
                transport: PreviewCallTransport(),
                integratesWithSystem: false
            ),
            installationBootstrapper: PreviewInstallationBootstrapper()
        )
    }
}
#endif
