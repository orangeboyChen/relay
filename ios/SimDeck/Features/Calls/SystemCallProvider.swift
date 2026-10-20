// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

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

final class DisabledSystemCallProvider: SystemCallProviding {
    func setDelegate(_ delegate: (any CXProviderDelegate)?, queue: DispatchQueue?) {}

    func reportNewIncomingCall(
        with UUID: UUID,
        update: CXCallUpdate,
        completion: @escaping @Sendable (Error?) -> Void
    ) {
        completion(nil)
    }

    func reportCall(with UUID: UUID, updated update: CXCallUpdate) {}
    func reportCall(with UUID: UUID, endedAt dateEnded: Date?, reason endedReason: CXCallEndedReason) {}
    func reportOutgoingCall(with UUID: UUID, startedConnectingAt dateStartedConnecting: Date?) {}
    func reportOutgoingCall(with UUID: UUID, connectedAt dateConnected: Date?) {}
}

@MainActor
protocol SystemCallControlling: AnyObject {
    func request(_ transaction: CXTransaction) async throws
}

extension CXCallController: SystemCallControlling {}
