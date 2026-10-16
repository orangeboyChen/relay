import AVFAudio
import Foundation
import Observation

struct CallAudioRoute: Identifiable, Equatable, Sendable {
    let id: String
    let name: String
    let kind: Kind
    let isSelectable: Bool

    enum Kind: Equatable, Sendable {
        case receiver
        case speaker
        case wiredHeadphones
        case airPods
        case airPodsPro
        case airPodsMax
        case bluetooth
        case airPlay
        case usb
        case car
        case external
    }

    var systemImage: String {
        switch kind {
        case .receiver: "iphone"
        case .speaker: "speaker.wave.3.fill"
        case .wiredHeadphones: "headphones"
        case .airPods: "airpods"
        case .airPodsPro: "airpodspro"
        case .airPodsMax: "airpodsmax"
        case .bluetooth: "wave.3.right.circle.fill"
        case .airPlay: "airplayaudio"
        case .usb: "cable.connector"
        case .car: "car.fill"
        case .external: "hifispeaker.fill"
        }
    }
}

struct CallAudioPortSnapshot: Equatable, Sendable {
    let id: String
    let name: String
    let typeRawValue: String
}

struct CallAudioSessionSnapshot: Equatable, Sendable {
    let outputs: [CallAudioPortSnapshot]
    let availableInputs: [CallAudioPortSnapshot]
}

@MainActor
protocol CallAudioRouteSource: AnyObject {
    var routeChangeNotification: Notification.Name? { get }
    var routeChangeObject: AnyObject? { get }

    func snapshot() -> CallAudioSessionSnapshot
    func selectRoute(id: String) throws
}

@MainActor
@Observable
final class CallAudioRouteController {
    private(set) var availableRoutes: [CallAudioRoute]
    private(set) var currentRoute: CallAudioRoute

    @ObservationIgnored private let source: any CallAudioRouteSource
    @ObservationIgnored private var routeChangeObservation: RouteChangeObservation?

    init(
        source: any CallAudioRouteSource = SystemCallAudioRouteSource(),
        notificationCenter: NotificationCenter = .default
    ) {
        self.source = source
        routeChangeObservation = nil
        let routes = Self.routes(from: source.snapshot())
        availableRoutes = routes.available
        currentRoute = routes.current

        if let notification = source.routeChangeNotification {
            routeChangeObservation = RouteChangeObservation(
                notificationCenter: notificationCenter,
                name: notification,
                object: source.routeChangeObject
            ) { [weak self] in
                Task { @MainActor [weak self] in
                    self?.refresh()
                }
            }
        }
    }

    func refresh() {
        let routes = Self.routes(from: source.snapshot())
        availableRoutes = routes.available
        currentRoute = routes.current
    }

    func select(_ route: CallAudioRoute) throws {
        guard route.isSelectable else { return }
        try source.selectRoute(id: route.id)
        refresh()
    }

    static func routeKind(typeRawValue: String, name: String) -> CallAudioRoute.Kind {
        let normalizedName = name.folding(
            options: [.caseInsensitive, .diacriticInsensitive],
            locale: .current
        )
        if normalizedName.contains("airpods max") {
            return .airPodsMax
        }
        if normalizedName.contains("airpods pro") {
            return .airPodsPro
        }
        if normalizedName.contains("airpods") {
            return .airPods
        }

        switch AVAudioSession.Port(rawValue: typeRawValue) {
        case .builtInReceiver, .builtInMic:
            return .receiver
        case .builtInSpeaker:
            return .speaker
        case .headphones, .headsetMic:
            return .wiredHeadphones
        case .bluetoothHFP, .bluetoothA2DP, .bluetoothLE:
            return .bluetooth
        case .airPlay:
            return .airPlay
        case .usbAudio:
            return .usb
        case .carAudio:
            return .car
        default:
            return .external
        }
    }

    private static func routes(
        from snapshot: CallAudioSessionSnapshot
    ) -> (available: [CallAudioRoute], current: CallAudioRoute) {
        let receiver = CallAudioRoute(
            id: SystemCallAudioRouteSource.receiverID,
            name: receiverName(in: snapshot),
            kind: .receiver,
            isSelectable: true
        )
        let speaker = CallAudioRoute(
            id: SystemCallAudioRouteSource.speakerID,
            name: speakerName(in: snapshot),
            kind: .speaker,
            isSelectable: true
        )

        let externalInputs = snapshot.availableInputs
            .filter { AVAudioSession.Port(rawValue: $0.typeRawValue) != .builtInMic }
            .map { port in
                CallAudioRoute(
                    id: SystemCallAudioRouteSource.inputPrefix + port.id,
                    name: port.name,
                    kind: routeKind(typeRawValue: port.typeRawValue, name: port.name),
                    isSelectable: true
                )
            }

        let output = snapshot.outputs.first
        let current: CallAudioRoute
        if let output {
            let kind = routeKind(typeRawValue: output.typeRawValue, name: output.name)
            switch kind {
            case .receiver:
                current = receiver
            case .speaker:
                current = speaker
            default:
                current = externalInputs.first(where: {
                    matches(output: output, route: $0, inputs: snapshot.availableInputs)
                }) ?? CallAudioRoute(
                    id: "output:\(output.id)",
                    name: output.name,
                    kind: kind,
                    isSelectable: false
                )
            }
        } else {
            current = receiver
        }

        var available = [receiver, speaker] + externalInputs
        if !available.contains(where: { $0.id == current.id }) {
            available.append(current)
        }
        return (available, current)
    }

    private static func matches(
        output: CallAudioPortSnapshot,
        route: CallAudioRoute,
        inputs: [CallAudioPortSnapshot]
    ) -> Bool {
        guard route.id.hasPrefix(SystemCallAudioRouteSource.inputPrefix) else { return false }
        let inputID = String(route.id.dropFirst(SystemCallAudioRouteSource.inputPrefix.count))
        guard let input = inputs.first(where: { $0.id == inputID }) else { return false }
        return input.id == output.id
            || input.name.localizedCaseInsensitiveCompare(output.name) == .orderedSame
            || routeKind(typeRawValue: input.typeRawValue, name: input.name)
                == routeKind(typeRawValue: output.typeRawValue, name: output.name)
    }

    private static func receiverName(in snapshot: CallAudioSessionSnapshot) -> String {
        snapshot.outputs.first(where: {
            AVAudioSession.Port(rawValue: $0.typeRawValue) == .builtInReceiver
        })?.name ?? "iPhone"
    }

    private static func speakerName(in snapshot: CallAudioSessionSnapshot) -> String {
        snapshot.outputs.first(where: {
            AVAudioSession.Port(rawValue: $0.typeRawValue) == .builtInSpeaker
        })?.name ?? "Speaker"
    }
}

@MainActor
final class SystemCallAudioRouteSource: CallAudioRouteSource {
    static let receiverID = "local.receiver"
    static let speakerID = "local.speaker"
    static let inputPrefix = "input:"

    private let audioSession: AVAudioSession

    init(audioSession: AVAudioSession = .sharedInstance()) {
        self.audioSession = audioSession
    }

    var routeChangeNotification: Notification.Name? {
        AVAudioSession.routeChangeNotification
    }

    var routeChangeObject: AnyObject? {
        audioSession
    }

    func snapshot() -> CallAudioSessionSnapshot {
        CallAudioSessionSnapshot(
            outputs: audioSession.currentRoute.outputs.map(Self.snapshot),
            availableInputs: (audioSession.availableInputs ?? []).map(Self.snapshot)
        )
    }

    func selectRoute(id: String) throws {
        if id == Self.speakerID {
            try audioSession.setPreferredInput(builtInMicrophone())
            try audioSession.overrideOutputAudioPort(.speaker)
            return
        }

        try audioSession.overrideOutputAudioPort(.none)
        if id == Self.receiverID {
            try audioSession.setPreferredInput(builtInMicrophone())
            return
        }

        guard id.hasPrefix(Self.inputPrefix) else { return }
        let portID = String(id.dropFirst(Self.inputPrefix.count))
        guard let input = audioSession.availableInputs?.first(where: { $0.uid == portID }) else {
            throw CallAudioRouteError.routeUnavailable
        }
        try audioSession.setPreferredInput(input)
    }

    private func builtInMicrophone() -> AVAudioSessionPortDescription? {
        audioSession.availableInputs?.first(where: { $0.portType == .builtInMic })
    }

    private static func snapshot(_ port: AVAudioSessionPortDescription) -> CallAudioPortSnapshot {
        CallAudioPortSnapshot(
            id: port.uid,
            name: port.portName,
            typeRawValue: port.portType.rawValue
        )
    }
}

enum CallAudioRouteError: LocalizedError {
    case routeUnavailable

    var errorDescription: String? {
        switch self {
        case .routeUnavailable:
            "The selected audio device is no longer available."
        }
    }
}

private final class RouteChangeObservation: @unchecked Sendable {
    private let notificationCenter: NotificationCenter
    private let observer: NSObjectProtocol

    init(
        notificationCenter: NotificationCenter,
        name: Notification.Name,
        object: AnyObject?,
        handler: @escaping @Sendable () -> Void
    ) {
        self.notificationCenter = notificationCenter
        observer = notificationCenter.addObserver(
            forName: name,
            object: object,
            queue: .main
        ) { _ in
            handler()
        }
    }

    deinit {
        notificationCenter.removeObserver(observer)
    }
}
