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
    @State private var isMorePresented = false
    @State private var isAddPeoplePresented = false
    @State private var isContactCardPresented = false
    @State private var hasAdditionalCall = false
    @State private var contactCardNumber = ""
    @State private var enteredDTMFDigits = ""
    @State private var presentedError: InCallPresentedError?
    @Namespace private var moreMenuNamespace

    var body: some View {
        GlassEffectContainer(spacing: 40) {
            ZStack {
                InCallBackdrop()
                    .overlay {
                        InCallMoreBackdrop()
                            .opacity(isMorePresented ? 1 : 0)
                    }
                    .ignoresSafeArea()

                if let call = coordinator.activeCall {
                    VStack(spacing: 0) {
                        CallIdentityHeader(
                            call: call,
                            line: line(for: call),
                            referenceDate: referenceDate,
                            keypadVisible: isKeypadVisible,
                            enteredDTMFDigits: enteredDTMFDigits
                        )
                        .padding(.top, 53)
                        .opacity(isMorePresented ? 0.48 : 1)

                        ZStack(alignment: .top) {
                            if isKeypadVisible {
                                InCallKeypad { digit in
                                    enteredDTMFDigits.append(contentsOf: digit)
                                    perform {
                                        try await coordinator.playDTMF(digit)
                                    }
                                }
                                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                                .padding(.top, 105)
                                .transition(inCallContentTransition)
                                .opacity(isMorePresented ? 0 : 1)
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
                                    onAudioRoute: { route in
                                        do {
                                            try coordinator.audioRoutes.select(route)
                                        } catch {
                                            presentedError = InCallPresentedError(
                                                message: "Could not change the audio route."
                                            )
                                        }
                                    },
                                    isMorePresented: isMorePresented,
                                    isCallEnded: call.status.isEnded
                                )
                                .padding(.bottom, 21)
                                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
                                .transition(inCallContentTransition)
                            }
                        }
                        .frame(maxHeight: .infinity, alignment: .top)
                        .allowsHitTesting(!isMorePresented)

                        InCallKeypadFooter(
                            keypadVisible: isKeypadVisible,
                            canUseKeypad: coordinator.capabilities.supportsDTMF,
                            isCallEnded: call.status.isEnded,
                            isMorePresented: isMorePresented,
                            moreMenuNamespace: moreMenuNamespace,
                            onMore: {
                                withAnimation(moreMenuAnimation) {
                                    isMorePresented = true
                                }
                            },
                            onKeypad: {
                                withAnimation(motionAnimation) {
                                    enteredDTMFDigits = ""
                                    isKeypadVisible = true
                                }
                            },
                            onEnd: {
                                Task {
                                    try? await coordinator.endCall()
                                }
                            },
                            onHide: {
                                withAnimation(motionAnimation) {
                                    enteredDTMFDigits = ""
                                    isKeypadVisible = false
                                }
                            }
                        )
                        .allowsHitTesting(!isMorePresented)
                    }
                    .padding(.horizontal, 24)
                    .padding(.bottom, 16)
                }
            }
            .overlay {
                if let call = coordinator.activeCall, isMorePresented {
                    ZStack {
                        Color.clear
                            .contentShape(Rectangle())
                            .ignoresSafeArea()
                            .onTapGesture {
                                withAnimation(moreMenuAnimation) {
                                    isMorePresented = false
                                }
                            }

                        VStack {
                            Spacer(minLength: 0)
                            InCallMorePanel(
                                isConnected: call.status.connectedAt != nil,
                                hasAdditionalCall: hasAdditionalCall,
                                namespace: moreMenuNamespace,
                                onAddPeople: {
                                    isMorePresented = false
                                    isAddPeoplePresented = true
                                },
                                onContactCard: {
                                    contactCardNumber = call.remoteNumber
                                    isMorePresented = false
                                    isContactCardPresented = true
                                },
                                onMerge: {
                                    isMorePresented = false
                                    perform {
                                        try await coordinator.mergeCalls()
                                        hasAdditionalCall = false
                                    }
                                },
                            )
                        }
                        .ignoresSafeArea(edges: .bottom)
                    }
                }
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
        .onChange(of: coordinator.activeCall?.id, initial: true) { _, _ in
            enteredDTMFDigits = ""
            isMorePresented = false
            hasAdditionalCall = false
        }
        .sheet(isPresented: $isContactCardPresented) {
            SystemUnknownContactView(phoneNumber: contactCardNumber)
        }
        .sheet(isPresented: $isAddPeoplePresented) {
            SystemContactNumberPicker(
                onSelect: { number in
                    isAddPeoplePresented = false
                    perform {
                        try await coordinator.addParticipant(number: DialString.normalized(number))
                        hasAdditionalCall = true
                    }
                },
                onCancel: {
                    isAddPeoplePresented = false
                }
            )
        }
        .preferredColorScheme(.dark)
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private func line(for call: ActiveCallSession) -> DeviceLineIdentity? {
        lines.first(where: { $0.id == call.lineID })
    }

    private var motionAnimation: Animation? {
        reduceMotion ? nil : .easeInOut(duration: 0.2)
    }

    private var moreMenuAnimation: Animation? {
        reduceMotion ? nil : .smooth(duration: 0.2)
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
    let keypadVisible: Bool
    let enteredDTMFDigits: String

    var body: some View {
        VStack(spacing: 6) {
            HStack(spacing: 6) {
                if let line {
                    SIMBadge(line: line, size: 18, color: .white)
                }

                CallStatusText(status: call.status, referenceDate: referenceDate)
            }
            .font(.title3.weight(.semibold))
            .foregroundStyle(.white.opacity(0.68))

            CallNumberDisplay(
                originalNumber: call.title,
                enteredDigits: ""
            )
            .offset(y: -4)

        }
        .opacity(showsEnteredDigits ? 0 : 1)
        .accessibilityHidden(showsEnteredDigits)
        .overlay(alignment: .top) {
            if showsEnteredDigits {
                CallNumberDisplay(
                    originalNumber: call.title,
                    enteredDigits: enteredDTMFDigits
                )
            }
        }
    }

    private var showsEnteredDigits: Bool {
        keypadVisible && !enteredDTMFDigits.isEmpty
    }
}

enum CallNumberDisplayValue {
    static func text(originalNumber: String, enteredDigits: String) -> String {
        enteredDigits.isEmpty ? originalNumber : enteredDigits
    }
}

private struct CallNumberDisplay: View {
    let originalNumber: String
    let enteredDigits: String

    private let fontSizes: [CGFloat] = [35, 32, 29, 26, 23, 20]

    var body: some View {
        let value = CallNumberDisplayValue.text(
            originalNumber: originalNumber,
            enteredDigits: enteredDigits
        )

        ViewThatFits(in: .horizontal) {
            ForEach(fontSizes, id: \.self) { size in
                Text(verbatim: value)
                    .font(.system(size: size, weight: .semibold))
                    .foregroundStyle(.white)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }

            Text(verbatim: value)
                .font(.system(size: 17, weight: .semibold))
                .foregroundStyle(.white)
                .lineLimit(1)
                .truncationMode(enteredDigits.isEmpty ? .tail : .head)
                .frame(maxWidth: .infinity, alignment: .trailing)
        }
        .frame(maxWidth: .infinity, alignment: .center)
        .accessibilityLabel(Text(verbatim: value))
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

private struct InCallMoreBackdrop: View {
    var body: some View {
        LinearGradient(
            stops: [
                .init(color: Color(red: 0.253, green: 0.253, blue: 0.253), location: 0),
                .init(color: Color(red: 0.245, green: 0.253, blue: 0.253), location: 0.23),
                .init(color: Color(red: 0.209, green: 0.233, blue: 0.241), location: 0.35),
                .init(color: Color(red: 0.143, green: 0.194, blue: 0.210), location: 0.47),
                .init(color: Color(red: 0.100, green: 0.166, blue: 0.182), location: 0.59),
                .init(color: Color(red: 0.092, green: 0.147, blue: 0.162), location: 0.72),
                .init(color: Color(red: 0.127, green: 0.151, blue: 0.159), location: 0.82),
                .init(color: Color(red: 0.155, green: 0.162, blue: 0.162), location: 1),
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
            Text("Call Ended")
        case .ended:
            Text("Call Ended")
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
    let onAudioRoute: (CallAudioRoute) -> Void
    let isMorePresented: Bool
    let isCallEnded: Bool

    var body: some View {
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
                systemImage: "mic.slash.fill",
                symbolSize: 26,
                isSelected: call.isMuted,
                isEnabled: capabilities.supportsMuting,
                selectedForegroundStyle: Color(
                    red: 183.0 / 255.0,
                    green: 21.0 / 255.0,
                    blue: 24.0 / 255.0
                ),
                selectedBackgroundStyle: .white,
                action: onMute
            )
        }
        .opacity(isMorePresented ? 0 : 1)
        .allowsHitTesting(!isCallEnded)
        .disabled(isCallEnded)
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
    }
}

private struct InCallMoreMenu: View {
    let isPresented: Bool
    let namespace: Namespace.ID
    let action: () -> Void

    var body: some View {
        if isPresented {
            CallControlLabel(
                title: "More",
                systemImage: "ellipsis",
                symbolSize: 26,
                foregroundStyle: .white,
                backgroundStyle: .white.opacity(0.16)
            )
            .hidden()
        } else {
            Button(action: action) {
                VStack(spacing: 8) {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 26, weight: .medium))
                        .foregroundStyle(.white)
                        .frame(width: 80, height: 80)
                        .glassEffect(.regular.interactive(), in: Circle())
                        .glassEffectID("moreSurface", in: namespace)
                        .glassEffectTransition(.matchedGeometry)

                    Text("More")
                        .font(.callout)
                        .foregroundStyle(.white)
                        .lineLimit(1)
                        .offset(y: -3)
                }
                .frame(width: 84)
            }
            .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.92))
            .accessibilityLabel("More")
        }
    }
}

private struct InCallMorePanel: View {
    let isConnected: Bool
    let hasAdditionalCall: Bool
    let namespace: Namespace.ID
    let onAddPeople: () -> Void
    let onContactCard: () -> Void
    let onMerge: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            if isConnected {
                Button(action: onAddPeople) {
                    InCallMoreRow(title: "Add People", icon: .addPeople)
                        .frame(height: 67)
                }
                .buttonStyle(.plain)
            }

            Button(action: onContactCard) {
                InCallMoreRow(title: "Contact Card", icon: .contactCard)
                    .frame(height: 67)
            }
            .buttonStyle(.plain)

            if isConnected && hasAdditionalCall {
                Rectangle()
                    .fill(.white.opacity(0.18))
                    .frame(height: 0.5)
                    .padding(.vertical, 8)

                Button(action: onMerge) {
                    InCallMoreRow(title: "Merge Calls", icon: .addPeople)
                        .frame(height: 67)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 32)
        .padding(.top, 17)
        .padding(.bottom, 17)
        .glassEffect(
            .regular,
            in: RoundedRectangle(cornerRadius: 42, style: .continuous)
        )
        .glassEffectID("moreSurface", in: namespace)
        .glassEffectTransition(.matchedGeometry)
        .padding(.horizontal, 8)
        .padding(.bottom, 8)
        .accessibilityElement(children: .contain)
    }
}

private struct InCallMoreRow: View {
    let title: LocalizedStringKey
    var subtitle: LocalizedStringKey? = nil
    let icon: InCallMoreIcon.Kind

    var body: some View {
        HStack(spacing: 12) {
            InCallMoreIcon(kind: icon)

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(.white)

                if let subtitle {
                    Text(subtitle)
                        .font(.system(size: 15))
                        .foregroundStyle(.white.opacity(0.52))
                }
            }

            Spacer(minLength: 0)
        }
        .contentShape(Rectangle())
    }
}

private struct InCallMoreIcon: View {
    enum Kind {
        case addPeople
        case contactCard
    }

    let kind: Kind

    var body: some View {
        ZStack {
            switch kind {
            case .addPeople:
                Circle()
                    .fill(Color(red: 97.0 / 255.0, green: 193.0 / 255.0, blue: 249.0 / 255.0))
                Image(systemName: "person.crop.circle.badge.plus")
                    .font(.system(size: 23, weight: .regular))
                    .foregroundStyle(.white)
                    .offset(x: 1, y: 1)
            case .contactCard:
                PersonAvatar(displayName: nil, size: 42)
            }
        }
        .frame(width: 42, height: 42)
        .accessibilityHidden(true)
    }
}

private struct InCallEndButton: View {
    let action: () -> Void
    var isEnabled = true

    var body: some View {
        Button(action: action) {
            CallControlLabel(
                title: "End",
                systemImage: "phone.down.fill",
                symbolSize: 26,
                foregroundStyle: .white,
                backgroundStyle: Color(
                    red: 183.0 / 255.0,
                    green: 21.0 / 255.0,
                    blue: 24.0 / 255.0
                )
            )
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
        .disabled(!isEnabled)
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
    var selectedForegroundStyle: Color = .black
    var selectedBackgroundStyle: Color = .white
    let action: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Button(action: action) {
            CallControlLabel(
                title: title,
                systemImage: systemImage,
                symbolSize: symbolSize,
                foregroundStyle: isSelected ? selectedForegroundStyle : .white,
                backgroundStyle: isSelected ? selectedBackgroundStyle : .white.opacity(0.16),
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
    let keypadVisible: Bool
    let canUseKeypad: Bool
    let isCallEnded: Bool
    let isMorePresented: Bool
    let moreMenuNamespace: Namespace.ID
    let onMore: () -> Void
    let onKeypad: () -> Void
    let onEnd: () -> Void
    let onHide: () -> Void

    var body: some View {
        Group {
            if keypadVisible {
                ZStack {
                    endButton

                    Button("Hide Keypad", action: onHide)
                        .font(.body)
                        .foregroundStyle(.white)
                        .frame(minWidth: 96, minHeight: 44)
                        .offset(x: 114)
                }
                .frame(height: 80)
            } else {
                HStack(alignment: .top, spacing: 36) {
                    InCallMoreMenu(
                        isPresented: isMorePresented,
                        namespace: moreMenuNamespace,
                        action: onMore
                    )

                    InCallEndButton(action: onEnd, isEnabled: !isCallEnded)
                        .frame(width: 84)
                        .opacity(isMorePresented ? 0 : 1)

                    CallControlButton(
                        title: "Keypad",
                        systemImage: "circle.grid.3x3.fill",
                        symbolSize: 25.5,
                        isSelected: false,
                        isEnabled: canUseKeypad,
                        action: onKeypad
                    )
                    .opacity(isMorePresented ? 0 : 1)
                }
                .frame(height: 80, alignment: .top)
            }
        }
        .allowsHitTesting(!isCallEnded)
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
    }

    private var endButton: some View {
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
        .disabled(isCallEnded)
        .accessibilityLabel("End call")
    }
}

private struct InCallPresentedError: Identifiable {
    let id = UUID()
    var title: LocalizedStringResource = "Call Control Failed"
    let message: LocalizedStringResource
    var endsFailedCall = false
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
