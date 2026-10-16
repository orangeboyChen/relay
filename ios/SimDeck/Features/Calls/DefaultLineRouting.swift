import Foundation
import Intents

struct ExternalDialRequest: Identifiable, Equatable {
    let id: UUID
    let number: String

    init(id: UUID = UUID(), number: String) {
        self.id = id
        self.number = number
    }
}

enum DefaultCallingRequestParser {
    static func number(from activity: NSUserActivity) -> String? {
        if let intent = activity.interaction?.intent as? INStartCallIntent,
           let number = number(from: intent) {
            return number
        }
        return activity.webpageURL.flatMap(number(from:))
    }

    static func number(from intent: INStartCallIntent) -> String? {
        let contacts = intent.contacts ?? []
        let callbackParticipants = intent.callRecordToCallBack?.participants ?? []

        return (contacts + callbackParticipants)
            .lazy
            .compactMap(\.personHandle?.value)
            .map(DialString.normalized)
            .first(where: DialString.isValid)
    }

    static func number(from url: URL) -> String? {
        guard url.scheme?.lowercased() == "tel" else { return nil }

        let encodedNumber = url.absoluteString.dropFirst("tel:".count)
        let decodedNumber = String(encodedNumber).removingPercentEncoding ?? String(encodedNumber)
        let number = DialString.normalized(String(decodedNumber.drop(while: { $0 == "/" })))
        return DialString.isValid(number) ? number : nil
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
