// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

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
            InCallBackdrop()
                .ignoresSafeArea()

            if let call = coordinator.activeCall {
                VStack(spacing: 0) {
                    CallIdentityHeader(
                        call: call,
                        line: line(for: call),
                        referenceDate: referenceDate
                    )
                        .padding(.top, 40)

                    Spacer(minLength: 40)

                    if isKeypadVisible {
                        InCallKeypad { digit in
                            perform {
                                try await coordinator.playDTMF(digit)
                            }
                        }
                        .transition(keypadTransition)

                        Spacer(minLength: 24)

                        InCallEndButton {
                            Task {
                                try? await coordinator.endCall()
                            }
                        }

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
                            },
                            onEnd: {
                                Task {
                                    try? await coordinator.endCall()
                                }
                            }
                        )
                        .transition(keypadTransition)
                    }
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 18)
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
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private func line(for call: ActiveCallSession) -> DeviceLineIdentity? {
        lines.first(where: { $0.id == call.lineID })
    }

    private var motionAnimation: Animation? {
        reduceMotion ? nil : .easeInOut(duration: 0.18)
    }

    private var keypadTransition: AnyTransition {
        reduceMotion ? .identity : .opacity
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
        VStack(spacing: 6) {
            HStack(spacing: 6) {
                if let line {
                    SIMBadge(line: line, size: 18)
                }

                CallStatusText(status: call.status, referenceDate: referenceDate)
            }
            .font(.title3)
            .foregroundStyle(.white.opacity(0.68))

            Text(call.title)
                .font(.system(size: 40, weight: .regular))
                .foregroundStyle(.white)
                .lineLimit(2)
                .minimumScaleFactor(0.65)
                .multilineTextAlignment(.center)
                .accessibilityLabel(call.title)

        }
    }
}

private struct InCallBackdrop: View {
    var body: some View {
        LinearGradient(
            colors: [
                Color(red: 0.43, green: 0.34, blue: 0.33),
                Color(red: 0.26, green: 0.36, blue: 0.32),
                Color(red: 0.06, green: 0.27, blue: 0.21),
            ],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
        .overlay(.ultraThinMaterial.opacity(0.12))
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
    let onEnd: () -> Void

    var body: some View {
        VStack(spacing: 18) {
            HStack(spacing: 36) {
                CallAudioRouteMenu(
                    controller: audioRoutes,
                    onSelect: onAudioRoute
                )
                CallControlButton(
                    title: "FaceTime",
                    systemImage: "video.fill",
                    isSelected: false,
                    isEnabled: false,
                    action: {}
                )
                CallControlButton(
                    title: "Mute",
                    systemImage: call.isMuted ? "mic.slash.fill" : "mic.slash",
                    isSelected: call.isMuted,
                    isEnabled: capabilities.supportsMuting,
                    action: onMute
                )
            }

            HStack(spacing: 36) {
                InCallMoreMenu(
                    isHeld: call.status.isHeld,
                    canHold: capabilities.supportsHolding && call.status.connectedAt != nil,
                    canSwap: capabilities.supportsSwapping,
                    onHold: onHold
                )

                InCallEndButton(action: onEnd)

                CallControlButton(
                    title: "Keypad",
                    systemImage: "circle.grid.3x3.fill",
                    isSelected: false,
                    isEnabled: capabilities.supportsDTMF,
                    action: onKeypad
                )
            }
        }
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
    }
}

private struct InCallMoreMenu: View {
    let isHeld: Bool
    let canHold: Bool
    let canSwap: Bool
    let onHold: () -> Void

    @State private var isPresented = false

    var body: some View {
        Button {
            isPresented = true
        } label: {
            CallControlLabel(
                title: "More",
                systemImage: "ellipsis",
                foregroundStyle: .white,
                backgroundStyle: .white.opacity(0.16)
            )
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.92))
        .accessibilityLabel("More")
        .popover(isPresented: $isPresented, attachmentAnchor: .rect(.bounds), arrowEdge: .bottom) {
            VStack(spacing: 0) {
                Button {
                    isPresented = false
                    onHold()
                } label: {
                    Label(
                        isHeld ? "Resume" : "Hold",
                        systemImage: isHeld ? "play.fill" : "pause.fill"
                    )
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .frame(minHeight: 44)
                }
                .disabled(!canHold)

                if canSwap {
                    Divider()

                    Button {} label: {
                        Label("Swap", systemImage: "arrow.trianglehead.swap")
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .frame(minHeight: 44)
                    }
                    .disabled(true)
                }
            }
            .font(.body)
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .frame(width: 184)
            .presentationCompactAdaptation(.popover)
        }
    }
}

private struct InCallEndButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            CallControlLabel(
                title: "End",
                systemImage: "phone.down.fill",
                foregroundStyle: .white,
                backgroundStyle: .red
            )
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
        .accessibilityLabel("End call")
    }
}

private struct CallAudioRouteMenu: View {
    let controller: CallAudioRouteController
    let onSelect: (CallAudioRoute) -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var primaryRoutes: [CallAudioRoute] {
        var routes = controller.availableRoutes.filter {
            $0.kind == .receiver || $0.kind == .speaker
        }
        let externalRoute = controller.currentRoute.kind == .receiver
            || controller.currentRoute.kind == .speaker
            ? controller.availableRoutes.first(where: {
                $0.kind != .receiver && $0.kind != .speaker
            })
            : controller.currentRoute
        if let externalRoute, !routes.contains(where: { $0.id == externalRoute.id }) {
            routes.append(externalRoute)
        }
        return routes
    }

    private var overflowRoutes: [CallAudioRoute] {
        controller.availableRoutes.filter { route in
            !primaryRoutes.contains(where: { $0.id == route.id })
        }
    }

    var body: some View {
        Menu {
            if !overflowRoutes.isEmpty {
                Menu {
                    ForEach(overflowRoutes.reversed()) { route in
                        Button {
                            onSelect(route)
                        } label: {
                            Label(route.name, systemImage: route.systemImage)
                        }
                        .disabled(!route.isSelectable)
                    }
                } label: {
                    Label("Show More", systemImage: "chevron.right")
                }
                Divider()
            }

            Picker(
                "Audio Output",
                selection: Binding(
                    get: { controller.currentRoute.id },
                    set: { routeID in
                        guard let route = controller.availableRoutes.first(where: {
                            $0.id == routeID
                        }) else { return }
                        onSelect(route)
                    }
                )
            ) {
                ForEach(primaryRoutes.reversed()) { route in
                    Label(route.name, systemImage: route.systemImage)
                        .tag(route.id)
                        .disabled(!route.isSelectable)
                }
            }
        } label: {
            CallControlLabel(
                title: "Audio",
                systemImage: controller.currentRoute.controlSystemImage,
                foregroundStyle: controller.currentRoute.isControlSelected ? .black : .white,
                backgroundStyle: controller.currentRoute.isControlSelected
                    ? .white
                    : .white.opacity(0.16)
            )
            .contentTransition(
                reduceMotion ? .identity : .symbolEffect(.replace)
            )
            .animation(
                reduceMotion ? nil : .snappy,
                value: controller.currentRoute.id
            )
        }
        .buttonStyle(InCallControlButtonStyle(pressedScale: 0.92))
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
            CallControlLabel(
                title: title,
                systemImage: systemImage,
                foregroundStyle: isSelected ? .black : .white,
                backgroundStyle: isSelected ? .white : .white.opacity(0.16),
                isEnabled: isEnabled
            )
            .contentTransition(
                reduceMotion ? .identity : .symbolEffect(.replace)
            )
        }
        .buttonStyle(InCallControlButtonStyle(pressedScale: 0.92))
        .disabled(!isEnabled)
        .animation(reduceMotion ? nil : .snappy, value: isSelected)
        .accessibilityValue(isSelected ? "On" : "Off")
    }
}

private struct CallControlLabel: View {
    let title: LocalizedStringKey
    let systemImage: String
    let foregroundStyle: Color
    let backgroundStyle: Color
    var isEnabled = true

    var body: some View {
        VStack(spacing: 8) {
            ZStack {
                Circle()
                    .fill(backgroundStyle)

                Image(systemName: systemImage)
                    .font(.system(size: 29, weight: .medium))
                    .foregroundStyle(foregroundStyle)
            }
            .frame(width: 80, height: 80)
            .opacity(isEnabled ? 1 : 0.35)

            Text(title)
                .font(.callout)
                .foregroundStyle(.white)
                .lineLimit(1)
        }
        .frame(width: 84)
    }
}

private struct InCallControlButtonStyle: ButtonStyle {
    var pressedScale: CGFloat

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(reduceMotion || !configuration.isPressed ? 1 : pressedScale)
            .opacity(configuration.isPressed ? 0.78 : 1)
            .animation(reduceMotion ? nil : .snappy, value: configuration.isPressed)
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
