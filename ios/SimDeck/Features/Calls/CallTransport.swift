// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import AVFAudio
import Foundation

struct CallCapabilities: Equatable, Sendable {
    var supportsHolding: Bool
    var supportsMuting: Bool
    var supportsDTMF: Bool
    var supportsSwapping: Bool

    static let cellular = CallCapabilities(
        supportsHolding: true,
        supportsMuting: true,
        supportsDTMF: true,
        supportsSwapping: false
    )
}

enum CallTransportEvent: Equatable, Sendable {
    case ringing(callID: UUID)
    case connected(callID: UUID, at: Date)
    case ended(callID: UUID, reason: CallEndReason)
}

enum CallEndReason: Equatable, Sendable {
    case remoteEnded
    case failed
    case unanswered
}

struct IncomingCallRegistration: Equatable, Sendable {
    let callID: UUID
    let lineID: UUID
    let callSecret: Data
    let verificationKey: Data
}

@MainActor
protocol CallTransport: AnyObject {
    var capabilities: CallCapabilities { get }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void)
    func registerIncomingCall(_ call: IncomingCallRegistration) throws
    func discardIncomingCall(callID: UUID)
    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws
    func answerIncomingCall(callID: UUID) async throws
    func rejectIncomingCall(callID: UUID) async throws
    func endCall(callID: UUID) async throws
    func setHeld(_ isHeld: Bool, callID: UUID) async throws
    func setMuted(_ isMuted: Bool, callID: UUID) async throws
    func playDTMF(_ digits: String, callID: UUID) async throws
    func audioSessionDidActivate(_ audioSession: AVAudioSession) async
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async
    func reset() async
}

@MainActor
protocol CallLineRouting: AnyObject {
    func updateLineRoutes(_ lines: [DeviceLineIdentity])
    func deviceID(for lineID: UUID) -> UUID
}

@MainActor
protocol CallNetworkPathHandling: AnyObject {
    func networkPathDidChange(isAvailable: Bool)
}

enum CallTransportError: LocalizedError {
    case notConfigured

    var errorDescription: String? {
        switch self {
        case .notConfigured:
            String(localized: "The server call connection is not configured.")
        }
    }
}

@MainActor
final class UnavailableCallTransport: CallTransport {
    let capabilities = CallCapabilities.cellular

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {}

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {
        throw CallTransportError.notConfigured
    }

    func discardIncomingCall(callID: UUID) {}

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        throw CallTransportError.notConfigured
    }

    func answerIncomingCall(callID: UUID) async throws { throw CallTransportError.notConfigured }
    func rejectIncomingCall(callID: UUID) async throws { throw CallTransportError.notConfigured }
    func endCall(callID: UUID) async throws {}
    func setHeld(_ isHeld: Bool, callID: UUID) async throws {}
    func setMuted(_ isMuted: Bool, callID: UUID) async throws {}
    func playDTMF(_ digits: String, callID: UUID) async throws {}
    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async {}
}

#if DEBUG
@MainActor
final class PreviewCallTransport: CallTransport {
    let capabilities = CallCapabilities.cellular
    private var eventHandler: (@MainActor (CallTransportEvent) -> Void)?

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {
        eventHandler = handler
    }

    func registerIncomingCall(_ call: IncomingCallRegistration) throws {}
    func discardIncomingCall(callID: UUID) {}

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        eventHandler?(.ringing(callID: callID))
        eventHandler?(.connected(callID: callID, at: PreviewFixtures.now))
    }

    func answerIncomingCall(callID: UUID) async throws {
        eventHandler?(.connected(callID: callID, at: PreviewFixtures.now))
    }

    func rejectIncomingCall(callID: UUID) async throws {
        eventHandler?(.ended(callID: callID, reason: .unanswered))
    }

    func endCall(callID: UUID) async throws {
        eventHandler?(.ended(callID: callID, reason: .remoteEnded))
    }

    func setHeld(_ isHeld: Bool, callID: UUID) async throws {}
    func setMuted(_ isMuted: Bool, callID: UUID) async throws {}
    func playDTMF(_ digits: String, callID: UUID) async throws {}
    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async {}
}
#endif
