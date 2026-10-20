// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import Intents

enum ExternalDialLineSelection: Equatable {
    case automatic
    case preferred(UUID)
}

struct ExternalDialRequest: Identifiable, Equatable {
    let id: UUID
    let number: String
    let lineSelection: ExternalDialLineSelection

    init(
        id: UUID = UUID(),
        number: String,
        lineSelection: ExternalDialLineSelection = .automatic
    ) {
        self.id = id
        self.number = number
        self.lineSelection = lineSelection
    }
}

struct DefaultCallingRequest: Equatable {
    let number: String
    let callRecordIdentifier: String?
    let isCallBack: Bool
}

enum DefaultCallingRequestParser {
    static func request(from activity: NSUserActivity) -> DefaultCallingRequest? {
        if let intent = activity.interaction?.intent as? INStartCallIntent,
           let request = request(from: intent) {
            return request
        }
        return activity.webpageURL.flatMap(request(from:))
    }

    static func request(from intent: INStartCallIntent) -> DefaultCallingRequest? {
        let contacts = intent.contacts ?? []
        let callbackParticipants = intent.callRecordToCallBack?.participants ?? []

        guard let number = (callbackParticipants + contacts)
            .lazy
            .compactMap(\.personHandle?.value)
            .map(DialString.normalized)
            .first(where: DialString.isValid) else {
            return nil
        }
        return DefaultCallingRequest(
            number: number,
            callRecordIdentifier: intent.callRecordToCallBack?.identifier,
            isCallBack: intent.callRecordToCallBack != nil
        )
    }

    static func request(from url: URL) -> DefaultCallingRequest? {
        guard url.scheme?.lowercased() == "tel" else { return nil }

        let encodedNumber = url.absoluteString.dropFirst("tel:".count)
        let decodedNumber = String(encodedNumber).removingPercentEncoding ?? String(encodedNumber)
        let number = DialString.normalized(String(decodedNumber.drop(while: { $0 == "/" })))
        guard DialString.isValid(number) else { return nil }
        return DefaultCallingRequest(
            number: number,
            callRecordIdentifier: nil,
            isCallBack: false
        )
    }
}

enum OutgoingCallLineResolution: Equatable {
    case ready(DeviceLineIdentity)
    case selectionRequired([DeviceLineIdentity])
    case unavailable
}

struct OutgoingCallLineRouter {
    static func resolve(
        lines: [DeviceLineIdentity],
        preferredLineID: UUID?
    ) -> OutgoingCallLineResolution {
        let onlineLines = lines.filter(\.isOnline)

        if let preferredLineID,
           let preferredLine = onlineLines.first(where: { $0.id == preferredLineID }) {
            return .ready(preferredLine)
        }
        if onlineLines.count == 1, let onlyLine = onlineLines.first {
            return .ready(onlyLine)
        }
        if onlineLines.isEmpty {
            return .unavailable
        }
        return .selectionRequired(onlineLines)
    }

    static func resolve(
        lines: [DeviceLineIdentity],
        requiredLineID: UUID
    ) -> OutgoingCallLineResolution {
        let onlineLines = lines.filter(\.isOnline)
        if let requiredLine = onlineLines.first(where: { $0.id == requiredLineID }) {
            return .ready(requiredLine)
        }
        return onlineLines.isEmpty ? .unavailable : .selectionRequired(onlineLines)
    }
}

protocol DefaultLineStoring: AnyObject {
    var callLineID: UUID? { get set }
    var messageLineID: UUID? { get set }
}

final class UserDefaultsDefaultLineStore: DefaultLineStoring {
    private enum Key {
        static let callLineID = "sim-deck.default-call-line-id.v1"
        static let messageLineID = "sim-deck.default-message-line-id.v1"
    }

    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    var callLineID: UUID? {
        get { uuid(forKey: Key.callLineID) }
        set { set(newValue, forKey: Key.callLineID) }
    }

    var messageLineID: UUID? {
        get { uuid(forKey: Key.messageLineID) }
        set { set(newValue, forKey: Key.messageLineID) }
    }

    private func uuid(forKey key: String) -> UUID? {
        defaults.string(forKey: key).flatMap(UUID.init(uuidString:))
    }

    private func set(_ value: UUID?, forKey key: String) {
        if let value {
            defaults.set(value.uuidString.lowercased(), forKey: key)
        } else {
            defaults.removeObject(forKey: key)
        }
    }
}
