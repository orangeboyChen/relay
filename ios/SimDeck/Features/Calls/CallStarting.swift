import Foundation

@MainActor
protocol CallStarting: AnyObject {
    func startCall(to number: String, using lineID: UUID) async throws
}

@MainActor
protocol CallControlling: AnyObject {
    func endCall() async throws
    func setHeld(_ isHeld: Bool) async throws
    func setMuted(_ isMuted: Bool) async throws
    func playDTMF(_ digits: String) async throws
    func setSpeakerEnabled(_ isEnabled: Bool) throws
}
