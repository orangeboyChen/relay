// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

@MainActor
protocol CallStarting: AnyObject {
    func startCall(to number: String, using lineID: UUID) async throws
}

@MainActor
protocol CallControlling: AnyObject {
    func endCall() async throws
    func addParticipant(number: String) async throws
    func mergeCalls() async throws
    func setHeld(_ isHeld: Bool) async throws
    func setMuted(_ isMuted: Bool) async throws
    func playDTMF(_ digits: String) async throws
}
