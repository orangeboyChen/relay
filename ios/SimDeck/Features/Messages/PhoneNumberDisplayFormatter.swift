// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum PhoneNumberDisplayFormatter {
    static func string(from input: String) -> String {
        let trimmed = input.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalized = DialString.normalized(trimmed)
        guard DialString.isValid(normalized),
              !normalized.contains("*"),
              !normalized.contains("#") else {
            return trimmed
        }

        if normalized.hasPrefix("+86") {
            return formatChineseNumber(String(normalized.dropFirst(3)), countryPrefix: "+86 ")
        }
        if normalized.hasPrefix("+1"), normalized.dropFirst(2).count == 10 {
            return formatNorthAmericanNumber(String(normalized.dropFirst(2)), countryPrefix: "+1 ")
        }
        if normalized.count == 11, isChineseMobile(normalized) {
            return groupedChineseMobile(normalized)
        }

        return trimmed
    }

    private static func formatChineseNumber(
        _ nationalNumber: String,
        countryPrefix: String
    ) -> String {
        if nationalNumber.count == 11, isChineseMobile(nationalNumber) {
            return countryPrefix + groupedChineseMobile(nationalNumber)
        }

        if nationalNumber.count == 10, usesTwoDigitAreaCode(nationalNumber) {
            let areaCode = nationalNumber.prefix(2)
            let subscriber = nationalNumber.dropFirst(2)
            return countryPrefix + "(\(areaCode)) \(groupSubscriber(subscriber))"
        }

        if nationalNumber.count == 11 {
            let areaCode = nationalNumber.prefix(3)
            let subscriber = nationalNumber.dropFirst(3)
            return countryPrefix + "(\(areaCode)) \(groupSubscriber(subscriber))"
        }

        return countryPrefix.trimmingCharacters(in: .whitespaces) + nationalNumber
    }

    private static func formatNorthAmericanNumber(
        _ nationalNumber: String,
        countryPrefix: String
    ) -> String {
        let areaCode = nationalNumber.prefix(3)
        let exchange = nationalNumber.dropFirst(3).prefix(3)
        let subscriber = nationalNumber.suffix(4)
        return countryPrefix + "(\(areaCode)) \(exchange)-\(subscriber)"
    }

    private static func groupedChineseMobile(_ number: String) -> String {
        let prefix = number.prefix(3)
        let middle = number.dropFirst(3).prefix(4)
        let suffix = number.suffix(4)
        return "\(prefix) \(middle) \(suffix)"
    }

    private static func groupSubscriber<S: StringProtocol>(_ subscriber: S) -> String {
        guard subscriber.count == 8 else { return String(subscriber) }
        return "\(subscriber.prefix(4)) \(subscriber.suffix(4))"
    }

    private static func isChineseMobile<S: StringProtocol>(_ number: S) -> Bool {
        guard number.count == 11 else { return false }
        let prefix = number.prefix(2)
        guard prefix.first == "1", let second = prefix.last else { return false }
        return ("3"..."9").contains(String(second))
    }

    private static func usesTwoDigitAreaCode(_ number: String) -> Bool {
        let areaCode = number.prefix(2)
        return ["10", "20", "21", "22", "23", "24", "25", "27", "28", "29"]
            .contains(String(areaCode))
    }
}
