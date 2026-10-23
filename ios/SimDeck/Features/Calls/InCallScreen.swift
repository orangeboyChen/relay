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
                    .padding(.top, 53)

                    if isKeypadVisible {
                        InCallKeypad { digit in
                            perform {
                                try await coordinator.playDTMF(digit)
                            }
                        }
                        .padding(.top, 105)
                        .transition(inCallContentTransition)

                        InCallKeypadFooter(
                            onEnd: {
                                Task {
                                    try? await coordinator.endCall()
                                }
                            },
                            onHide: {
                                withAnimation(motionAnimation) {
                                    isKeypadVisible = false
                                }
                            }
                        )
                        .padding(.top, 48.5)

                        Spacer(minLength: 0)
                    } else {
                        Spacer(minLength: 40)

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
                        .transition(inCallContentTransition)
                    }
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 16)
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
        .preferredColorScheme(.dark)
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private func line(for call: ActiveCallSession) -> DeviceLineIdentity? {
        lines.first(where: { $0.id == call.lineID })
    }

    private var motionAnimation: Animation? {
        reduceMotion ? nil : .easeInOut(duration: 0.18)
    }

    private var inCallContentTransition: AnyTransition {
        reduceMotion
            ? .identity
            : .asymmetric(insertion: .opacity, removal: .identity)
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
                .font(.system(size: 35, weight: .regular))
                .foregroundStyle(.white)
                .lineLimit(2)
                .minimumScaleFactor(0.65)
                .multilineTextAlignment(.center)
                .offset(y: -4)
                .accessibilityLabel(call.title)

        }
    }
}

private struct InCallBackdrop: View {
    var body: some View {
        LinearGradient(
            stops: [
                .init(color: Color(red: 0.318, green: 0.420, blue: 0.588), location: 0.00),
                .init(color: Color(red: 0.345, green: 0.439, blue: 0.576), location: 0.13),
                .init(color: Color(red: 0.376, green: 0.447, blue: 0.557), location: 0.25),
                .init(color: Color(red: 0.392, green: 0.443, blue: 0.529), location: 0.31),
                .init(color: Color(red: 0.345, green: 0.396, blue: 0.494), location: 0.38),
                .init(color: Color(red: 0.302, green: 0.349, blue: 0.451), location: 0.44),
                .init(color: Color(red: 0.282, green: 0.329, blue: 0.424), location: 0.50),
                .init(color: Color(red: 0.192, green: 0.251, blue: 0.353), location: 0.56),
                .init(color: Color(red: 0.145, green: 0.180, blue: 0.278), location: 0.63),
                .init(color: Color(red: 0.133, green: 0.169, blue: 0.251), location: 0.69),
                .init(color: Color(red: 0.137, green: 0.173, blue: 0.243), location: 0.75),
                .init(color: Color(red: 0.137, green: 0.161, blue: 0.220), location: 0.82),
                .init(color: Color(red: 0.137, green: 0.153, blue: 0.204), location: 1.00),
            ],
            startPoint: .top,
            endPoint: .bottom
        )
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
        VStack(spacing: 21) {
            HStack(spacing: 36) {
                CallAudioRouteMenu(
                    controller: audioRoutes,
                    onSelect: onAudioRoute
                )
                CallControlButton(
                    title: "FaceTime",
                    systemImage: "video.badge.questionmark.fill",
                    symbolSize: 22,
                    isSelected: false,
                    isEnabled: false,
                    action: {}
                )
                CallControlButton(
                    title: "Mute",
                    systemImage: call.isMuted ? "mic.slash.fill" : "mic.slash",
                    symbolSize: 26,
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
                    symbolSize: 25.5,
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
                symbolSize: 26,
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
                symbolSize: 26,
                foregroundStyle: .white,
                backgroundStyle: Color(
                    red: 212.0 / 255.0,
                    green: 8.0 / 255.0,
                    blue: 9.0 / 255.0
                )
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
                symbolSize: 25.5,
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
    var symbolSize: CGFloat = 26
    let isSelected: Bool
    let isEnabled: Bool
    let action: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Button(action: action) {
            CallControlLabel(
                title: title,
                systemImage: systemImage,
                symbolSize: symbolSize,
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
    var symbolSize: CGFloat = 26
    let foregroundStyle: Color
    let backgroundStyle: Color
    var isEnabled = true

    var body: some View {
        VStack(spacing: 8) {
            CallControlCircle(
                systemImage: systemImage,
                symbolSize: symbolSize,
                foregroundStyle: foregroundStyle,
                tint: backgroundStyle,
                isEnabled: isEnabled
            )

            Text(title)
                .font(.callout)
                .foregroundStyle(.white)
                .lineLimit(1)
                .offset(y: -3)
        }
        .frame(width: 84)
    }
}

private struct CallControlCircle: View {
    let systemImage: String
    var symbolSize: CGFloat = 26
    let foregroundStyle: Color
    let tint: Color
    var isEnabled = true

    var body: some View {
        ZStack {
            Circle()
                .fill(tint)
                .overlay {
                    Circle()
                        .strokeBorder(
                            LinearGradient(
                                colors: [
                                    Color(
                                        red: 0.80,
                                        green: 0.87,
                                        blue: 0.99
                                    )
                                    .opacity(0.82),
                                    .white.opacity(0.18),
                                    .white.opacity(0.10),
                                ],
                                startPoint: .top,
                                endPoint: .bottom
                            ),
                            lineWidth: 1
                        )
                }

            Image(systemName: systemImage)
                .font(.system(size: symbolSize, weight: .medium))
                .foregroundStyle(foregroundStyle)
        }
        .frame(width: 80, height: 80)
        .contentShape(Circle())
            .opacity(isEnabled ? 1 : 0.55)
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
    private let rows = [
        [InCallKey(symbol: "1", letters: ""), InCallKey(symbol: "2", letters: "ABC"), InCallKey(symbol: "3", letters: "DEF")],
        [InCallKey(symbol: "4", letters: "GHI"), InCallKey(symbol: "5", letters: "JKL"), InCallKey(symbol: "6", letters: "MNO")],
        [InCallKey(symbol: "7", letters: "PQRS"), InCallKey(symbol: "8", letters: "TUV"), InCallKey(symbol: "9", letters: "WXYZ")],
        [InCallKey(symbol: "*", letters: ""), InCallKey(symbol: "0", letters: "+"), InCallKey(symbol: "#", letters: "")],
    ]

    @State private var feedbackTrigger = 0

    var body: some View {
        VStack(spacing: 18.5) {
            ForEach(rows, id: \.self) { row in
                HStack(spacing: 40) {
                    ForEach(row, id: \.self) { key in
                        Button {
                            onDigit(key.symbol)
                            feedbackTrigger += 1
                        } label: {
                            InCallKeyLabel(key: key)
                        }
                        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
                        .accessibilityLabel(Text(verbatim: key.accessibilityLabel))
                    }
                }
            }
        }
        .frame(maxWidth: .infinity)
        .sensoryFeedback(.selection, trigger: feedbackTrigger)
    }
}

private struct InCallKey: Hashable {
    let symbol: String
    let letters: String

    var symbolFontSize: CGFloat {
        switch symbol {
        case "*": 58
        case "#": 32
        default: 38
        }
    }

    var accessibilityLabel: String {
        letters.isEmpty ? symbol : "\(symbol), \(letters)"
    }
}

private struct InCallKeyLabel: View {
    let key: InCallKey

    var body: some View {
        VStack(spacing: -5) {
            Text(verbatim: key.symbol)
                .font(.system(size: key.symbolFontSize, weight: .regular))
                .scaleEffect(x: 1, y: key.symbol == "*" ? 1.19 : 1)
                .offset(y: key.symbol == "*" ? 20 : 0)

            Text(verbatim: key.letters)
                .font(.system(size: 9, weight: .bold))
                .tracking(key.letters.count > 1 ? 4 : 0)
                .frame(height: 11)
                .opacity(key.letters.isEmpty ? 0 : 1)
                .accessibilityHidden(true)
        }
        .foregroundStyle(.white)
        .offset(y: -3)
        .frame(width: 80, height: 80)
        .background(
            Color(red: 0.82, green: 0.96, blue: 1).opacity(0.20),
            in: Circle()
        )
        .overlay {
            Circle()
                .strokeBorder(
                    LinearGradient(
                        colors: [
                            Color(
                                red: 232.0 / 255.0,
                                green: 250.0 / 255.0,
                                blue: 1
                            ),
                            .white.opacity(0.24),
                            .white.opacity(0.12),
                        ],
                        startPoint: .top,
                        endPoint: .bottom
                    ),
                    lineWidth: 1.25
                )
        }
        .contentShape(Circle())
    }
}

private struct InCallKeypadFooter: View {
    let onEnd: () -> Void
    let onHide: () -> Void

    var body: some View {
        ZStack {
            Button(action: onEnd) {
                CallControlCircle(
                    systemImage: "phone.down.fill",
                    symbolSize: 26,
                    foregroundStyle: .white,
                    tint: Color(
                        red: 183.0 / 255.0,
                        green: 21.0 / 255.0,
                        blue: 24.0 / 255.0
                    )
                )
            }
            .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
            .accessibilityLabel("End call")

            Button("Hide Keypad", action: onHide)
                .font(.body)
                .foregroundStyle(.white)
                .frame(minWidth: 96, minHeight: 44)
                .offset(x: 114)
        }
        .frame(height: 80)
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
