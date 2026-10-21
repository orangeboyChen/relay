// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct DialerContactMatch: Identifiable, Equatable {
    let contact: ContactRecord
    let phoneNumber: ContactPhoneNumber

    var id: String { "\(contact.id)::\(phoneNumber.id)" }
}

struct DialerHighlightedPhoneNumber: Equatable {
    let prefix: String
    let match: String
    let suffix: String
}

enum DialerContactMatcher {
    static func matches(number: String, contacts: [ContactRecord]) -> [DialerContactMatch] {
        let query = normalizedDigits(number)
        guard !query.isEmpty else { return [] }

        return contacts.flatMap { contact in
            contact.phoneNumbers.compactMap { phoneNumber in
                let candidate = normalizedDigits(phoneNumber.value)
                guard candidate.contains(query) else { return nil }
                return DialerContactMatch(contact: contact, phoneNumber: phoneNumber)
            }
        }
    }

    static func exactMatch(number: String, contacts: [ContactRecord]) -> DialerContactMatch? {
        let query = normalizedDigits(number)
        guard !query.isEmpty else { return nil }
        return contacts.lazy
            .flatMap { contact in
                contact.phoneNumbers.map { DialerContactMatch(contact: contact, phoneNumber: $0) }
            }
            .first { normalizedDigits($0.phoneNumber.value) == query }
    }

    static func highlightedPhoneNumber(
        _ phoneNumber: String,
        matching number: String
    ) -> DialerHighlightedPhoneNumber? {
        let query = normalizedDigits(number)
        guard !query.isEmpty else { return nil }

        let digitIndices = phoneNumber.indices.filter { phoneNumber[$0].isNumber }
        let candidateDigits = String(digitIndices.map { phoneNumber[$0] })
        let prefixLength = countryCodePrefixLength(
            in: candidateDigits,
            originalValue: phoneNumber
        )
        let searchableDigits = String(candidateDigits.dropFirst(prefixLength))
        guard let digitRange = searchableDigits.range(of: query) else { return nil }

        let lowerOffset = prefixLength + searchableDigits.distance(
            from: searchableDigits.startIndex,
            to: digitRange.lowerBound
        )
        let upperOffset = prefixLength + searchableDigits.distance(
            from: searchableDigits.startIndex,
            to: digitRange.upperBound
        )
        guard lowerOffset < upperOffset,
              lowerOffset < digitIndices.count,
              upperOffset <= digitIndices.count else {
            return nil
        }

        let matchStart = digitIndices[lowerOffset]
        let lastDigit = digitIndices[upperOffset - 1]
        let matchEnd = phoneNumber.index(after: lastDigit)
        return DialerHighlightedPhoneNumber(
            prefix: String(phoneNumber[..<matchStart]),
            match: String(phoneNumber[matchStart..<matchEnd]),
            suffix: String(phoneNumber[matchEnd...])
        )
    }

    private static func normalizedDigits(_ value: String) -> String {
        let digits = value.filter(\.isNumber)
        return String(
            digits.dropFirst(
                countryCodePrefixLength(in: digits, originalValue: value)
            )
        )
    }

    private static func countryCodePrefixLength(
        in digits: String,
        originalValue: String? = nil
    ) -> Int {
        let trimmedValue = originalValue?.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmedValue?.hasPrefix("+86") == true, digits.hasPrefix("86") {
            return 2
        }
        if digits.hasPrefix("0086") {
            return 4
        }
        if digits.count == 13, digits.hasPrefix("86") {
            return 2
        }
        return 0
    }
}
