import SwiftUI

struct SettingsScreen: View {
    let model: AppModel

    var body: some View {
        List {
            Section {
                SettingsLink("Server", symbol: "server.rack") {
                    ServerSettingsScreen(model: model)
                }
                SettingsLink("Default Lines", symbol: "simcard.2") {
                    DefaultLineSettingsScreen(model: model)
                }
                SettingsLink("Notifications", symbol: "bell") {
                    NotificationSettingsScreen()
                }
                SettingsLink("Contacts", symbol: "person.crop.circle") {
                    ContactsSettingsScreen()
                }
                SettingsLink("Security", symbol: "lock.shield") {
                    SecuritySettingsScreen(model: model)
                }
                SettingsLink("Messages & Storage", symbol: "externaldrive") {
                    StorageSettingsScreen()
                }
                SettingsLink("Advanced", symbol: "gearshape.2") {
                    AdvancedSettingsScreen()
                }
                SettingsLink("About", symbol: "info.circle") {
                    AboutScreen()
                }
            }

            Section {
                NavigationLink("Delete Account & Server Data") {
                    DataDeletionScreen()
                }
                .foregroundStyle(.red)
            }
        }
        .navigationTitle("Settings")
    }
}

private struct SettingsLink<Destination: View>: View {
    let title: LocalizedStringKey
    let symbol: String
    @ViewBuilder let destination: () -> Destination

    init(
        _ title: LocalizedStringKey,
        symbol: String,
        @ViewBuilder destination: @escaping () -> Destination
    ) {
        self.title = title
        self.symbol = symbol
        self.destination = destination
    }

    var body: some View {
        NavigationLink {
            destination()
        } label: {
            Label(title, systemImage: symbol)
        }
    }
}

#if DEBUG
#Preview("Settings") {
    NavigationStack {
        SettingsScreen(model: .preview(selectedTab: .profile))
    }
}

#Preview("Settings - Accessibility Chinese") {
    NavigationStack {
        SettingsScreen(model: .preview(selectedTab: .profile))
    }
    .environment(\.dynamicTypeSize, .accessibility5)
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}
#endif
