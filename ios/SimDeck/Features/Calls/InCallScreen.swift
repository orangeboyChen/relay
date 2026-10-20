import SwiftUI

struct InCallScreen: View {
    let coordinator: CallKitCoordinator
    let lines: [DeviceLineIdentity]
    var referenceDate: Date?

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isKeypadVisible = false
    @State private var presentedError: InCallPresentedError?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            if let call = coordinator.activeCall {
                VStack(spacing: 0) {
                    CallIdentityHeader(
                        call: call,
                        line: line(for: call),
                        referenceDate: referenceDate
                    )
                        .padding(.top, 58)

                    Spacer(minLength: 28)

                    if isKeypadVisible {
                        InCallKeypad { digit in
                            perform {
                                try await coordinator.playDTMF(digit)
                            }
                        }
                        .transition(keypadTransition)
                    } else {
                        InCallControls(
                            call: call,
                            capabilities: coordinator.capabilities,
                            audioRoutes: coordinator.audioRoutes,
                            onMute: {
                                perform {
                                    try await coordinator.setMuted(!call.isMuted)
                                }
                            },
                            onKeypad: {
                                withAnimation(motionAnimation) {
                                    isKeypadVisible = true
                                }
                            },
                            onAudioRoute: { route in
                                do {
                                    try coordinator.audioRoutes.select(route)
                                } catch {
                                    presentedError = InCallPresentedError(
                                        message: "Could not change the audio route."
                                    )
                                }
                            },
                            onHold: {
                                perform {
                                    try await coordinator.setHeld(!call.status.isHeld)
                                }
                            }
                        )
                        .transition(keypadTransition)
                    }

                    Spacer(minLength: 30)

                    Button {
                        Task {
                            try? await coordinator.endCall()
                        }
                    } label: {
                        Image(systemName: "phone.down.fill")
                            .font(.system(size: 28, weight: .semibold))
                            .foregroundStyle(.white)
                            .frame(width: 72, height: 72)
                            .background(.red, in: Circle())
                    }
                    .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
                    .accessibilityLabel("End call")

                    if isKeypadVisible {
                        Button("Hide Keypad") {
                            withAnimation(motionAnimation) {
                                isKeypadVisible = false
                            }
                        }
                        .font(.headline)
                        .foregroundStyle(.white)
                        .frame(minHeight: 44)
                        .padding(.top, 10)
                    } else {
                        Color.clear.frame(height: 54)
                    }
                }
                .padding(.horizontal, 28)
                .padding(.bottom, 26)
            }
        }
        .interactiveDismissDisabled()
        .alert(item: $presentedError) { error in
            Alert(
                title: Text(error.title),
                message: Text(error.message),
                dismissButton: .default(Text("OK")) {
                    if error.endsFailedCall {
                        coordinator.dismissCallStartFailure()
                    }
                }
            )
        }
        .onChange(of: coordinator.callStartFailureID, initial: true) { _, callID in
            guard callID != nil else { return }
            presentedError = InCallPresentedError(
                title: "Call Failed",
                message: "Could not start the call.",
                endsFailedCall: true
            )
        }
    }

    private func line(for call: ActiveCallSession) -> DeviceLineIdentity? {
        lines.first(where: { $0.id == call.lineID })
    }

    private var motionAnimation: Animation? {
        reduceMotion ? nil : .snappy
    }

    private var keypadTransition: AnyTransition {
        reduceMotion
            ? .opacity
            : .scale(scale: 0.96).combined(with: .opacity)
    }

    private func perform(_ operation: @escaping @MainActor () async throws -> Void) {
        Task {
            do {
                try await operation()
            } catch {
                presentedError = InCallPresentedError(
                    message: "The call command could not be completed."
                )
            }
        }
    }
}

private struct CallIdentityHeader: View {
    let call: ActiveCallSession
    let line: DeviceLineIdentity?
    let referenceDate: Date?

    var body: some View {
        VStack(spacing: 10) {
            Text(call.title)
                .font(.largeTitle.weight(.semibold))
                .foregroundStyle(.white)
                .lineLimit(2)
                .minimumScaleFactor(0.65)
                .multilineTextAlignment(.center)

            CallStatusText(status: call.status, referenceDate: referenceDate)
                .font(.title3)
                .foregroundStyle(.white.opacity(0.72))

            if let line {
                HStack(spacing: 7) {
                    SIMBadge(line: line)
                    Text(line.displayName)
                        .lineLimit(1)
                }
                .font(.subheadline)
                .foregroundStyle(.white.opacity(0.78))
                .padding(.horizontal, 12)
                .frame(minHeight: 34)
                .background(.white.opacity(0.12), in: Capsule())
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
                .accessibilityElement(children: .combine)
            }
        }
    }
}

private struct CallStatusText: View {
    let status: ActiveCallStatus
    let referenceDate: Date?

    var body: some View {
        switch status {
        case .requesting:
            Text("Starting…")
        case .connecting:
            Text("Calling…")
        case .ringing:
            Text("Ringing…")
        case .active(let connectedAt):
            if let referenceDate {
                CallDurationText(connectedAt: connectedAt, currentDate: referenceDate)
            } else {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    CallDurationText(connectedAt: connectedAt, currentDate: context.date)
                }
            }
        case .held:
            Text("On Hold")
        case .ending:
            Text("Ending…")
        case .failed:
            Text("Call Failed")
        }
    }
}

private struct CallDurationText: View {
    let connectedAt: Date
    let currentDate: Date

    var body: some View {
        let duration = max(0, currentDate.timeIntervalSince(connectedAt))
        Text(verbatim: CallDurationFormatter.string(elapsed: duration))
            .monospacedDigit()
    }
}

enum CallDurationFormatter {
    static func string(elapsed: TimeInterval) -> String {
        let wholeSeconds = Int64(max(0, elapsed.rounded(.down)))
        let duration = Duration.seconds(wholeSeconds)
        if wholeSeconds >= 3_600 {
            return duration.formatted(
                .time(pattern: .hourMinuteSecond(padHourToLength: 2))
            )
        }
        return duration.formatted(
            .time(pattern: .minuteSecond(padMinuteToLength: 2))
        )
    }
}

private struct InCallControls: View {
    let call: ActiveCallSession
    let capabilities: CallCapabilities
    let audioRoutes: CallAudioRouteController
    let onMute: () -> Void
    let onKeypad: () -> Void
    let onAudioRoute: (CallAudioRoute) -> Void
    let onHold: () -> Void

    var body: some View {
        VStack(spacing: 26) {
            HStack(spacing: 24) {
                CallControlButton(
                    title: "Mute",
                    systemImage: call.isMuted ? "mic.slash.fill" : "mic.slash",
                    isSelected: call.isMuted,
                    isEnabled: capabilities.supportsMuting,
                    action: onMute
                )
                CallControlButton(
                    title: "Keypad",
                    systemImage: "circle.grid.3x3.fill",
                    isSelected: false,
                    isEnabled: capabilities.supportsDTMF,
                    action: onKeypad
                )
                CallAudioRouteMenu(
                    controller: audioRoutes,
                    onSelect: onAudioRoute
                )
            }

            HStack(spacing: 24) {
                CallControlButton(
                    title: call.status.isHeld ? "Resume" : "Hold",
                    systemImage: call.status.isHeld ? "play.fill" : "pause.fill",
                    isSelected: call.status.isHeld,
                    isEnabled: capabilities.supportsHolding && call.status.connectedAt != nil,
                    action: onHold
                )
                CallControlButton(
                    title: "Swap",
                    systemImage: "arrow.trianglehead.swap",
                    isSelected: false,
                    isEnabled: capabilities.supportsSwapping,
                    action: {}
                )
            }
        }
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
    }
}

private struct CallAudioRouteMenu: View {
    let controller: CallAudioRouteController
    let onSelect: (CallAudioRoute) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Menu {
            ForEach(controller.availableRoutes) { route in
                Button {
                    onSelect(route)
                } label: {
                    Label(route.name, systemImage: route.systemImage)
                }
                .disabled(!route.isSelectable || route.id == controller.currentRoute.id)
            }
        } label: {
            VStack(spacing: 8) {
                Image(systemName: controller.currentRoute.systemImage)
                    .font(.system(size: 27, weight: .medium))
                    .frame(width: 72, height: 72)
                    .foregroundStyle(.black)
                    .background(.white, in: Circle())
                    .contentTransition(
                        reduceMotion ? .identity : .symbolEffect(.replace)
                    )
                    .animation(
                        reduceMotion ? nil : .snappy,
                        value: controller.currentRoute.id
                    )

                Text("Audio")
                    .font(.caption)
                    .foregroundStyle(.white)
                    .lineLimit(1)
            }
            .frame(width: 84)
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.92))
        .accessibilityLabel("Audio")
        .accessibilityValue(Text(verbatim: controller.currentRoute.name))
    }
}

private struct CallControlButton: View {
    let title: LocalizedStringKey
    let systemImage: String
    let isSelected: Bool
    let isEnabled: Bool
    let action: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Button(action: action) {
            VStack(spacing: 8) {
                Image(systemName: systemImage)
                    .font(.system(size: 27, weight: .medium))
                    .frame(width: 72, height: 72)
                    .foregroundStyle(isSelected ? .black : .white)
                    .background(isSelected ? .white : .white.opacity(0.16), in: Circle())
                    .contentTransition(
                        reduceMotion ? .identity : .symbolEffect(.replace)
                    )

                Text(title)
                    .font(.caption)
                    .foregroundStyle(.white)
                    .lineLimit(1)
            }
            .frame(width: 84)
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.92))
        .disabled(!isEnabled)
        .opacity(isEnabled ? 1 : 0.35)
        .animation(reduceMotion ? nil : .snappy, value: isSelected)
        .accessibilityValue(isSelected ? "On" : "Off")
    }
}

private struct InCallKeypad: View {
    let onDigit: (String) -> Void
    private let keys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"]

    @State private var feedbackTrigger = 0

    var body: some View {
        LazyVGrid(
            columns: Array(repeating: GridItem(.flexible(), spacing: 18), count: 3),
            spacing: 14
        ) {
            ForEach(keys, id: \.self) { key in
                Button {
                    onDigit(key)
                    feedbackTrigger += 1
                } label: {
                    Text(verbatim: key)
                        .font(.system(size: 30, weight: .regular, design: .rounded))
                        .foregroundStyle(.white)
                        .frame(width: 66, height: 66)
                        .background(.white.opacity(0.16), in: Circle())
                }
                .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
                .accessibilityLabel(Text(verbatim: key))
            }
        }
        .frame(maxWidth: 300)
        .sensoryFeedback(.selection, trigger: feedbackTrigger)
    }
}

private struct InCallPresentedError: Identifiable {
    let id = UUID()
    var title: LocalizedStringResource = "Call Control Failed"
    let message: LocalizedStringResource
    var endsFailedCall = false
}

private extension ActiveCallStatus {
    var isHeld: Bool {
        if case .held = self { return true }
        return false
    }
}

#if DEBUG
#Preview("Active Call") {
    let call = ActiveCallSession(
        id: UUID(uuidString: "30000000-0000-0000-0000-000000000001")!,
        lineID: PreviewFixtures.devices[0].id,
        remoteNumber: "13800000000",
        displayName: "China Mobile",
        status: .active(connectedAt: PreviewFixtures.now.addingTimeInterval(-75)),
        isMuted: false
    )
    let coordinator = CallKitCoordinator(
        transport: PreviewCallTransport(),
        integratesWithSystem: false,
        initialCall: call,
        audioRoutes: CallAudioRouteController(source: PreviewCallAudioRouteSource())
    )

    InCallScreen(
        coordinator: coordinator,
        lines: PreviewFixtures.devices,
        referenceDate: PreviewFixtures.now
    )
}

#Preview("Call Connecting - Dark AX") {
    let call = ActiveCallSession(
        id: UUID(uuidString: "30000000-0000-0000-0000-000000000002")!,
        lineID: PreviewFixtures.devices[0].id,
        remoteNumber: "13500000000",
        displayName: nil,
        status: .connecting,
        isMuted: false
    )
    let coordinator = CallKitCoordinator(
        transport: PreviewCallTransport(),
        integratesWithSystem: false,
        initialCall: call,
        audioRoutes: CallAudioRouteController(
            source: PreviewCallAudioRouteSource(currentDeviceName: "AirPods Max")
        )
    )

    InCallScreen(
        coordinator: coordinator,
        lines: PreviewFixtures.devices,
        referenceDate: PreviewFixtures.now
    )
        .environment(\.dynamicTypeSize, .accessibility3)
}
#endif
