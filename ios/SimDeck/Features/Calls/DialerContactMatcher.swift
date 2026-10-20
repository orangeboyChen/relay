// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

struct DialerContactMatch: Identifiable, Equatable {
    let contact: ContactRecord
    let phoneNumber: ContactPhoneNumber

    var id: String { phoneNumber.id }
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

    private static func normalizedDigits(_ value: String) -> String {
        let digits = value.filter(\.isNumber)
        if digits.count == 13, digits.hasPrefix("86") {
            return String(digits.dropFirst(2))
        }
        if digits.count == 15, digits.hasPrefix("0086") {
            return String(digits.dropFirst(4))
        }
        return digits
    }
}
