// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import AVFAudio
import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class CallAudioRouteControllerTests: XCTestCase {
    func testBuildsVoiceRoutesAndUsesAirPodsProAsCurrentOutput() {
        let source = CallAudioRouteSourceFake(
            outputs: [port("pro-output", "Alex's AirPods Pro", .bluetoothHFP)],
            inputs: [
                port("mic", "iPhone Microphone", .builtInMic),
                port("pro-input", "Alex's AirPods Pro", .bluetoothHFP),
                port("max-input", "Studio AirPods Max", .bluetoothHFP)
            ]
        )

        let controller = CallAudioRouteController(source: source)

        XCTAssertEqual(controller.currentRoute.name, "Alex's AirPods Pro")
        XCTAssertEqual(controller.currentRoute.kind, .airPodsPro)
        XCTAssertEqual(controller.currentRoute.systemImage, "airpodspro")
        XCTAssertEqual(controller.currentRoute.controlSystemImage, "airpodspro")
        XCTAssertTrue(controller.currentRoute.isControlSelected)
        XCTAssertEqual(
            controller.availableRoutes.map(\.kind),
            [.receiver, .speaker, .airPodsPro, .airPodsMax]
        )
    }

    func testSelectsSpeakerThroughInjectedAudioSessionAndRefreshesCurrentRoute() throws {
        let source = CallAudioRouteSourceFake(
            outputs: [port("receiver", "iPhone", .builtInReceiver)],
            inputs: [port("mic", "iPhone Microphone", .builtInMic)]
        )
        let controller = CallAudioRouteController(source: source)
        let speaker = try XCTUnwrap(
            controller.availableRoutes.first(where: { $0.kind == .speaker })
        )

        try controller.select(speaker)

        XCTAssertEqual(source.selectedRouteIDs, [SystemCallAudioRouteSource.speakerID])
        XCTAssertEqual(controller.currentRoute.kind, .speaker)
        XCTAssertEqual(controller.currentRoute.systemImage, "speaker.wave.3.fill")
        XCTAssertTrue(controller.currentRoute.isControlSelected)
    }

    func testReceiverUsesSpeakerControlIconWithoutSelectedAppearance() {
        let source = CallAudioRouteSourceFake(
            outputs: [port("receiver", "iPhone", .builtInReceiver)],
            inputs: [port("mic", "iPhone Microphone", .builtInMic)]
        )

        let controller = CallAudioRouteController(source: source)

        XCTAssertEqual(controller.currentRoute.kind, .receiver)
        XCTAssertEqual(controller.currentRoute.controlSystemImage, "speaker.wave.3.fill")
        XCTAssertFalse(controller.currentRoute.isControlSelected)
    }

    func testMapsSupportedOutputTypesToDistinctSystemIcons() {
        let cases: [(AVAudioSession.Port, String, CallAudioRoute.Kind, String)] = [
            (.headphones, "EarPods", .wiredHeadphones, "headphones"),
            (.bluetoothHFP, "Generic Headset", .bluetooth, "wave.3.right.circle.fill"),
            (.airPlay, "Living Room", .airPlay, "airplayaudio"),
            (.usbAudio, "USB Interface", .usb, "cable.connector"),
            (.carAudio, "CarPlay", .car, "car.fill")
        ]

        for (portType, name, expectedKind, expectedImage) in cases {
            let kind = CallAudioRouteController.routeKind(
                typeRawValue: portType.rawValue,
                name: name
            )
            let route = CallAudioRoute(
                id: name,
                name: name,
                kind: kind,
                isSelectable: true
            )
            XCTAssertEqual(kind, expectedKind, name)
            XCTAssertEqual(route.systemImage, expectedImage, name)
        }
    }

    func testKeepsSystemSelectedOutputVisibleWhenItCannotBeSelectedDirectly() {
        let source = CallAudioRouteSourceFake(
            outputs: [port("hearing-aid", "Hearing Device", .bluetoothLE)],
            inputs: [port("mic", "iPhone Microphone", .builtInMic)]
        )

        let controller = CallAudioRouteController(source: source)

        XCTAssertEqual(controller.currentRoute.name, "Hearing Device")
        XCTAssertEqual(controller.currentRoute.kind, .bluetooth)
        XCTAssertFalse(controller.currentRoute.isSelectable)
        XCTAssertTrue(controller.availableRoutes.contains(controller.currentRoute))
    }

    private func port(
        _ id: String,
        _ name: String,
        _ type: AVAudioSession.Port
    ) -> CallAudioPortSnapshot {
        CallAudioPortSnapshot(id: id, name: name, typeRawValue: type.rawValue)
    }
}

@MainActor
private final class CallAudioRouteSourceFake: CallAudioRouteSource {
    private var outputs: [CallAudioPortSnapshot]
    private let inputs: [CallAudioPortSnapshot]
    private(set) var selectedRouteIDs: [String] = []

    init(outputs: [CallAudioPortSnapshot], inputs: [CallAudioPortSnapshot]) {
        self.outputs = outputs
        self.inputs = inputs
    }

    var routeChangeNotification: Notification.Name? { nil }
    var routeChangeObject: AnyObject? { nil }

    func snapshot() -> CallAudioSessionSnapshot {
        CallAudioSessionSnapshot(outputs: outputs, availableInputs: inputs)
    }

    func selectRoute(id: String) throws {
        selectedRouteIDs.append(id)
        switch id {
        case SystemCallAudioRouteSource.receiverID:
            outputs = [CallAudioPortSnapshot(
                id: "receiver",
                name: "iPhone",
                typeRawValue: AVAudioSession.Port.builtInReceiver.rawValue
            )]
        case SystemCallAudioRouteSource.speakerID:
            outputs = [CallAudioPortSnapshot(
                id: "speaker",
                name: "Speaker",
                typeRawValue: AVAudioSession.Port.builtInSpeaker.rawValue
            )]
        default:
            let inputID = String(id.dropFirst(SystemCallAudioRouteSource.inputPrefix.count))
            guard let input = inputs.first(where: { $0.id == inputID }) else {
                throw CallAudioRouteError.routeUnavailable
            }
            outputs = [input]
        }
    }
}
