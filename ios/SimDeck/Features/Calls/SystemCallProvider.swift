import CallKit
import Foundation

protocol SystemCallProviding: AnyObject {
    func setDelegate(_ delegate: (any CXProviderDelegate)?, queue: DispatchQueue?)
    func reportNewIncomingCall(
        with UUID: UUID,
        update: CXCallUpdate,
        completion: @escaping @Sendable (Error?) -> Void
    )
    func reportCall(with UUID: UUID, updated update: CXCallUpdate)
    func reportCall(with UUID: UUID, endedAt dateEnded: Date?, reason endedReason: CXCallEndedReason)
    func reportOutgoingCall(with UUID: UUID, startedConnectingAt dateStartedConnecting: Date?)
    func reportOutgoingCall(with UUID: UUID, connectedAt dateConnected: Date?)
}

extension CXProvider: SystemCallProviding {}
