import SwiftUI

struct OnboardingScreen: View {
    let model: AppModel

    @State private var step = OnboardingStep.welcome

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                TabView(selection: $step) {
                    OnboardingWelcomePage()
                        .tag(OnboardingStep.welcome)

                    OnboardingServerPage(model: model)
                        .tag(OnboardingStep.server)

                    OnboardingRecoveryPage(state: model.keyRecoveryState)
                        .tag(OnboardingStep.recovery)
                }
                .tabViewStyle(.page(indexDisplayMode: .never))

                OnboardingProgress(step: step)
                    .padding(.bottom, 16)

                OnboardingActions(
                    step: step,
                    onContinue: advance,
                    onBack: goBack
                )
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
            }
            .background(Color(uiColor: .systemGroupedBackground))
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Set Up Later") {
                        model.completeOnboarding()
                    }
                }
            }
        }
    }

    private func advance() {
        switch step {
        case .welcome:
            step = .server
        case .server:
            step = .recovery
        case .recovery:
            model.completeOnboarding()
        }
    }

    private func goBack() {
        switch step {
        case .welcome:
            break
        case .server:
            step = .welcome
        case .recovery:
            step = .server
        }
    }
}

private enum OnboardingStep: Int, CaseIterable {
    case welcome
    case server
    case recovery
}

private struct OnboardingWelcomePage: View {
    var body: some View {
        OnboardingPageLayout(
            systemImage: "simcard.2.fill",
            title: "Welcome to sim-deck",
            message: "Call and message through your own SIM devices while your numbers and conversations remain end-to-end encrypted."
        ) {
            VStack(spacing: 18) {
                OnboardingFeatureRow(
                    systemImage: "phone.fill",
                    title: "Your calls",
                    message: "Choose the SIM that places each call."
                )
                OnboardingFeatureRow(
                    systemImage: "message.fill",
                    title: "Your messages",
                    message: "Keep conversations separated by SIM."
                )
                OnboardingFeatureRow(
                    systemImage: "lock.shield.fill",
                    title: "Private by design",
                    message: "The server relays encrypted data it cannot read."
                )
            }
        }
    }
}

private struct OnboardingServerPage: View {
    let model: AppModel

    @Bindable private var connection: ServerConnectionManager
    @State private var password = ""
    @State private var signInMethod: SignInMethod?
    @State private var errorMessage: String?

    private enum SignInMethod {
        case password
        case passkey
    }

    init(model: AppModel) {
        self.model = model
        _connection = Bindable(wrappedValue: model.serverConnection)
    }

    var body: some View {
        OnboardingPageLayout(
            systemImage: "network",
            title: "Connect Your Server",
            message: "Your server manages trusted iPhones and SIM devices without receiving message or call content."
        ) {
            VStack(spacing: 14) {
                TextField("Server address", text: $connection.address)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .textContentType(.URL)
                    .disabled(connection.isSignedIn)
                    .onChange(of: connection.address) {
                        errorMessage = nil
                    }

                if !connection.isSignedIn {
                    SecureField("Admin bootstrap password", text: $password)
                        .textContentType(.password)

                    Button {
                        Task { await signInWithPassword() }
                    } label: {
                        if signInMethod == .password {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Text("Sign In")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(!canSignInWithPassword)

                    Button {
                        Task { await signInWithPasskey() }
                    } label: {
                        if signInMethod == .passkey {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                        } else {
                            Label("Sign In with Passkey", systemImage: "person.badge.key")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.large)
                    .disabled(!canSignInWithPasskey)
                }

                HStack(spacing: 8) {
                    ServerConnectionStatusLabel(state: connection.state)
                    Spacer(minLength: 0)
                }

                if let errorMessage {
                    Text(errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(16)
            .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18))
        }
    }

    private var canSignInWithPassword: Bool {
        signInMethod == nil
            && !connection.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !password.isEmpty
    }

    private var canSignInWithPasskey: Bool {
        signInMethod == nil
            && !connection.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func signInWithPassword() async {
        signInMethod = .password
        errorMessage = nil
        defer { signInMethod = nil }
        do {
            try await model.signInToServer(password: password)
            password = ""
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func signInWithPasskey() async {
        signInMethod = .passkey
        errorMessage = nil
        defer { signInMethod = nil }
        do {
            try await model.signInWithPasskey()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

private struct OnboardingRecoveryPage: View {
    let state: KeyRecoveryState

    var body: some View {
        OnboardingPageLayout(
            systemImage: "key.icloud.fill",
            title: "Protect App Reinstallation",
            message: "After reinstalling, sign in with your passkey and restore the encryption key from iCloud Keychain."
        ) {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 12) {
                    Image(systemName: recoveryIcon)
                        .font(.title2)
                        .foregroundStyle(recoveryColor)
                        .frame(width: 36, height: 36)

                    VStack(alignment: .leading, spacing: 3) {
                        Text("iCloud Keychain recovery")
                            .font(.headline)
                        KeyRecoveryStatusLabel(state: state)
                    }
                }

                Divider()

                Text("If iCloud recovery is unavailable, another trusted iPhone or the Linux host must approve this installation. A reinstalled app always receives a new installation identity.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .padding(16)
            .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18))
        }
    }

    private var recoveryIcon: String {
        switch state {
        case .checking: "icloud"
        case .available: "checkmark.icloud.fill"
        case .approvalRequired: "person.badge.key.fill"
        case .unavailable: "exclamationmark.icloud.fill"
        }
    }

    private var recoveryColor: Color {
        switch state {
        case .checking: .secondary
        case .available: .green
        case .approvalRequired: .orange
        case .unavailable: .red
        }
    }
}

private struct OnboardingPageLayout<Content: View>: View {
    let systemImage: String
    let title: LocalizedStringKey
    let message: LocalizedStringKey
    @ViewBuilder let content: Content

    init(
        systemImage: String,
        title: LocalizedStringKey,
        message: LocalizedStringKey,
        @ViewBuilder content: () -> Content
    ) {
        self.systemImage = systemImage
        self.title = title
        self.message = message
        self.content = content()
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 28) {
                VStack(spacing: 18) {
                    Image(systemName: systemImage)
                        .font(.system(size: 58, weight: .semibold))
                        .foregroundStyle(.tint)
                        .accessibilityHidden(true)

                    VStack(spacing: 10) {
                        Text(title)
                            .font(.largeTitle.bold())
                            .multilineTextAlignment(.center)

                        Text(message)
                            .font(.body)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                }

                content
                    .frame(maxWidth: 520)
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 24)
            .padding(.top, 40)
            .padding(.bottom, 24)
        }
    }
}

private struct OnboardingFeatureRow: View {
    let systemImage: String
    let title: LocalizedStringKey
    let message: LocalizedStringKey

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            Image(systemName: systemImage)
                .font(.title3)
                .foregroundStyle(.tint)
                .frame(width: 32, height: 32)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.headline)
                Text(message)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .combine)
    }
}

private struct OnboardingProgress: View {
    let step: OnboardingStep

    var body: some View {
        HStack(spacing: 8) {
            ForEach(OnboardingStep.allCases, id: \.rawValue) { candidate in
                Capsule()
                    .fill(candidate == step ? Color.accentColor : Color.secondary.opacity(0.28))
                    .frame(width: candidate == step ? 22 : 8, height: 8)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Setup progress")
        .accessibilityValue("Step \(step.rawValue + 1) of \(OnboardingStep.allCases.count)")
    }
}

private struct OnboardingActions: View {
    let step: OnboardingStep
    let onContinue: () -> Void
    let onBack: () -> Void

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 12) {
                backButton
                continueButton
            }

            VStack(spacing: 10) {
                continueButton
                backButton
            }
        }
    }

    private var backButton: some View {
        Button("Back", action: onBack)
            .buttonStyle(.bordered)
            .controlSize(.large)
            .frame(maxWidth: .infinity)
            .opacity(step == .welcome ? 0 : 1)
            .disabled(step == .welcome)
            .accessibilityHidden(step == .welcome)
    }

    private var continueButton: some View {
        Button(step == .recovery ? "Done" : "Continue", action: onContinue)
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .frame(maxWidth: .infinity)
    }
}

#if DEBUG
#Preview("Onboarding - Welcome") {
    OnboardingScreen(model: .preview(onboardingCompleted: false))
}

#Preview("Onboarding - Dark Chinese") {
    OnboardingScreen(
        model: .preview(
            keyRecoveryState: .approvalRequired,
            onboardingCompleted: false
        )
    )
    .preferredColorScheme(.dark)
    .environment(\.locale, Locale(identifier: "zh-Hans"))
}

#Preview("Onboarding - Accessibility") {
    OnboardingScreen(model: .preview(onboardingCompleted: false))
        .environment(\.dynamicTypeSize, .accessibility4)
}

#Preview("Onboarding - Passkey Sign In") {
    NavigationStack {
        OnboardingServerPage(model: .preview(onboardingCompleted: false))
            .padding()
            .background(Color(uiColor: .systemGroupedBackground))
    }
}
#endif
