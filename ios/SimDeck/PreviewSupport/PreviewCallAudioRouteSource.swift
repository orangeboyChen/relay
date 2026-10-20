// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

#if DEBUG
import AVFAudio
import Foundation

@MainActor
final class PreviewCallAudioRouteSource: CallAudioRouteSource {
    private var currentOutput: CallAudioPortSnapshot
    private let inputs: [CallAudioPortSnapshot]

    init(currentDeviceName: String = "AirPods Pro") {
        inputs = [
            CallAudioPortSnapshot(
                id: "preview-built-in",
                name: "iPhone Microphone",
                typeRawValue: AVAudioSession.Port.builtInMic.rawValue
            ),
            CallAudioPortSnapshot(
                id: "preview-airpods-pro",
                name: "AirPods Pro",
                typeRawValue: AVAudioSession.Port.bluetoothHFP.rawValue
            ),
            CallAudioPortSnapshot(
                id: "preview-airpods-max",
                name: "AirPods Max",
                typeRawValue: AVAudioSession.Port.bluetoothHFP.rawValue
            )
        ]
        currentOutput = CallAudioPortSnapshot(
            id: "preview-airpods-pro",
            name: currentDeviceName,
            typeRawValue: AVAudioSession.Port.bluetoothHFP.rawValue
        )
    }

    var routeChangeNotification: Notification.Name? { nil }
    var routeChangeObject: AnyObject? { nil }

    func snapshot() -> CallAudioSessionSnapshot {
        CallAudioSessionSnapshot(outputs: [currentOutput], availableInputs: inputs)
    }

    func selectRoute(id: String) throws {
        switch id {
        case SystemCallAudioRouteSource.receiverID:
            currentOutput = CallAudioPortSnapshot(
                id: "preview-receiver",
                name: "iPhone",
                typeRawValue: AVAudioSession.Port.builtInReceiver.rawValue
            )
        case SystemCallAudioRouteSource.speakerID:
            currentOutput = CallAudioPortSnapshot(
                id: "preview-speaker",
                name: "Speaker",
                typeRawValue: AVAudioSession.Port.builtInSpeaker.rawValue
            )
        default:
            let inputID = String(id.dropFirst(SystemCallAudioRouteSource.inputPrefix.count))
            guard let input = inputs.first(where: { $0.id == inputID }) else {
                throw CallAudioRouteError.routeUnavailable
            }
            currentOutput = input
        }
    }
}
#endif
