import CallKit
import Foundation

@MainActor
protocol CallStarting: AnyObject {
    func startCall(to number: String, using lineID: UUID) async throws
}

@MainActor
final class CallKitCallStarter: CallStarting {
    private let controller = CXCallController()

    func startCall(to number: String, using lineID: UUID) async throws {
        let handle = CXHandle(type: .phoneNumber, value: number)
        let action = CXStartCallAction(call: UUID(), handle: handle)
        action.isVideo = false
        try await controller.request(CXTransaction(action: action))
    }
}

#if DEBUG
@MainActor
final class PreviewCallStarter: CallStarting {
    private(set) var requests: [(number: String, lineID: UUID)] = []

    func startCall(to number: String, using lineID: UUID) async throws {
        requests.append((number, lineID))
    }
}
#endif
