// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct IncomingCallPresentation: Equatable, Sendable {
    let notificationID: UUID
    let callID: UUID
    let lineID: UUID
    let installationID: UUID
    let numericID: Int
    let remoteNumber: String
    let contactName: String?
    let callSecret: Data
    let deviceVerificationKey: Data

    var callerName: String {
        let caller = contactName.flatMap { $0.isEmpty ? nil : $0 } ?? remoteNumber
        return "\(caller) · SIM \(numericID)"
    }
}

@MainActor
protocol IncomingCallReporting: AnyObject {
    func reportIncomingCall(_ call: IncomingCallPresentation) async throws
    func reportIncomingCallAnsweredElsewhere(notificationID: UUID)
    func updateIncomingCallName(_ name: String?, callID: UUID)
}
