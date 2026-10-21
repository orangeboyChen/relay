// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum ContactAuthorizationState: Equatable, Sendable {
    case notDetermined
    case denied
    case limited
    case authorized
}

struct ContactPhoneNumber: Identifiable, Hashable, Sendable {
    let id: String
    let label: String
    let value: String

    var displayValue: String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? PhoneNumberDisplayFormatter.string(from: value) : trimmed
    }
}

struct ContactRecord: Identifiable, Hashable, Sendable {
    let id: String
    let givenName: String
    let familyName: String
    let organizationName: String
    let phoneNumbers: [ContactPhoneNumber]
    let thumbnailImageData: Data?

    var displayName: String {
        let personName = ContactNameFormatting.displayName(
            givenName: givenName,
            familyName: familyName
        )
        if !personName.isEmpty {
            return personName
        }
        if !organizationName.isEmpty {
            return organizationName
        }
        return phoneNumbers.first?.displayValue ?? String(localized: "Unnamed Contact")
    }

    var initials: String? {
        let parts = displayName.split(whereSeparator: { $0.isWhitespace })
        let characters = parts.prefix(2).compactMap(\.first)
        return characters.isEmpty ? nil : String(characters).uppercased()
    }

    var sectionTitle: String {
        guard let first = sortKey.first else { return "#" }
        let title = String(first).uppercased()
        return title.rangeOfCharacter(from: .letters) == nil ? "#" : title
    }

    var sortKey: String {
        ContactNameFormatting.sortKey(
            displayName: displayName,
            givenName: givenName,
            familyName: familyName
        )
    }

    func matches(_ query: String) -> Bool {
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else { return true }
        let queryDigits = query.filter(\.isNumber)
        return displayName.localizedCaseInsensitiveContains(query)
            || organizationName.localizedCaseInsensitiveContains(query)
            || phoneNumbers.contains {
                $0.value.localizedCaseInsensitiveContains(query)
                    || $0.displayValue.localizedCaseInsensitiveContains(query)
                    || (!queryDigits.isEmpty && $0.value.filter(\.isNumber).contains(queryDigits))
            }
    }

    func phoneNumber(matching value: String) -> ContactPhoneNumber? {
        let lookupKeys = Set(ContactPhoneNumberLookup.keys(for: value))
        guard !lookupKeys.isEmpty else { return nil }

        return phoneNumbers.first { phoneNumber in
            !lookupKeys.isDisjoint(with: ContactPhoneNumberLookup.keys(for: phoneNumber.value))
        }
    }
}

struct ContactSection: Identifiable, Equatable, Sendable {
    let id: String
    let title: String
    let contacts: [ContactRecord]
}

enum ContactListState: Equatable, Sendable {
    case permissionRequired
    case permissionDenied
    case loading
    case loaded([ContactSection])
    case failed
}

enum ContactDirectory {
    static func sections(for contacts: [ContactRecord], matching query: String = "") -> [ContactSection] {
        let visibleContacts = contacts
            .filter { $0.matches(query) }
            .sorted {
                $0.sortKey.localizedStandardCompare($1.sortKey) == .orderedAscending
            }
        let grouped = Dictionary(grouping: visibleContacts, by: \.sectionTitle)
        return grouped.keys
            .sorted { lhs, rhs in
                if lhs == "#" { return false }
                if rhs == "#" { return true }
                return lhs.localizedStandardCompare(rhs) == .orderedAscending
            }
            .map { title in
                ContactSection(id: title, title: title, contacts: grouped[title] ?? [])
            }
    }
}

enum ContactSectionIndexResolver {
    static let titles = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZ").map(String.init) + ["#"]
    static let visibleItemHeight: CGFloat = 11
    static var visibleTrackHeight: CGFloat {
        CGFloat(titles.count) * visibleItemHeight
    }

    static func index(at locationY: CGFloat, height: CGFloat, itemCount: Int) -> Int? {
        guard height > 0, itemCount > 0 else { return nil }
        let fraction = min(max(locationY / height, 0), 0.999_999)
        return min(Int(fraction * CGFloat(itemCount)), itemCount - 1)
    }

    static func targetTitle(for indexTitle: String, availableTitles: [String]) -> String? {
        guard !availableTitles.isEmpty else { return nil }
        if availableTitles.contains(indexTitle) {
            return indexTitle
        }
        if indexTitle == "#" {
            return availableTitles.last
        }
        return availableTitles.first {
            $0 != "#" && $0.localizedStandardCompare(indexTitle) != .orderedAscending
        } ?? availableTitles.last
    }
}

enum ContactNameFormatting {
    static func displayName(givenName: String, familyName: String) -> String {
        let givenName = givenName.trimmingCharacters(in: .whitespacesAndNewlines)
        let familyName = familyName.trimmingCharacters(in: .whitespacesAndNewlines)
        if usesHanNameOrder(givenName: givenName, familyName: familyName) {
            return familyName + givenName
        }
        return [givenName, familyName]
            .filter { !$0.isEmpty }
            .joined(separator: " ")
    }

    static func sortKey(
        displayName: String,
        givenName: String,
        familyName: String
    ) -> String {
        let source: String
        if usesHanNameOrder(givenName: givenName, familyName: familyName) {
            source = familyName + givenName
        } else {
            source = displayName
        }
        return source
            .applyingTransform(.toLatin, reverse: false)?
            .folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
            ?? source
    }

    private static func containsHanCharacters(_ value: String) -> Bool {
        value.unicodeScalars.contains { scalar in
            switch scalar.value {
            case 0x3400...0x4DBF, 0x4E00...0x9FFF, 0xF900...0xFAFF:
                true
            default:
                false
            }
        }
    }

    private static func usesHanNameOrder(givenName: String, familyName: String) -> Bool {
        let fullName = givenName + familyName
        return containsHanCharacters(fullName) && !containsLatinCharacters(fullName)
    }

    private static func containsLatinCharacters(_ value: String) -> Bool {
        value.unicodeScalars.contains { scalar in
            switch scalar.value {
            case 0x0041...0x005A, 0x0061...0x007A, 0x00C0...0x024F:
                true
            default:
                false
            }
        }
    }
}

struct ContactNameIndex: Equatable, Sendable {
    private let namesByNumber: [String: String]

    init(contacts: [ContactRecord] = []) {
        var namesByNumber: [String: String] = [:]
        for contact in contacts {
            for phoneNumber in contact.phoneNumbers {
                for key in ContactPhoneNumberLookup.keys(for: phoneNumber.value) {
                    namesByNumber[key, default: contact.displayName] = contact.displayName
                }
            }
        }
        self.namesByNumber = namesByNumber
    }

    func name(for phoneNumber: String) -> String? {
        ContactPhoneNumberLookup.keys(for: phoneNumber).lazy
            .compactMap { namesByNumber[$0] }
            .first
    }
}

struct ContactRecordIndex: Equatable, Sendable {
    private let contactsByNumber: [String: ContactRecord]

    init(contacts: [ContactRecord] = []) {
        var contactsByNumber: [String: ContactRecord] = [:]
        for contact in contacts {
            for phoneNumber in contact.phoneNumbers {
                for key in ContactPhoneNumberLookup.keys(for: phoneNumber.value) {
                    contactsByNumber[key, default: contact] = contact
                }
            }
        }
        self.contactsByNumber = contactsByNumber
    }

    func contact(for phoneNumber: String) -> ContactRecord? {
        ContactPhoneNumberLookup.keys(for: phoneNumber).lazy
            .compactMap { contactsByNumber[$0] }
            .first
    }
}

private enum ContactPhoneNumberLookup {
    static func keys(for phoneNumber: String) -> [String] {
        let digits = phoneNumber.filter(\.isNumber)
        guard !digits.isEmpty else { return [] }

        var keys = [digits]
        if digits.count == 13, digits.hasPrefix("86") {
            keys.append(String(digits.dropFirst(2)))
        } else if digits.count == 15, digits.hasPrefix("0086") {
            keys.append(String(digits.dropFirst(4)))
        } else if digits.count == 11 {
            keys.append("86" + digits)
            keys.append("0086" + digits)
        }
        return keys
    }
}
