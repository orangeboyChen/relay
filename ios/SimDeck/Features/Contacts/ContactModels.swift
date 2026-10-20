import Foundation

enum ContactAuthorizationState: Equatable, Sendable {
    case notDetermined
    case denied
    case limited
    case authorized
}

struct ContactPhoneNumber: Identifiable, Equatable, Sendable {
    let id: String
    let label: String
    let value: String

    var displayValue: String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? PhoneNumberDisplayFormatter.string(from: value) : trimmed
    }
}

struct ContactRecord: Identifiable, Equatable, Sendable {
    let id: String
    let givenName: String
    let familyName: String
    let organizationName: String
    let phoneNumbers: [ContactPhoneNumber]
    let thumbnailImageData: Data?

    var displayName: String {
        let personName = [givenName, familyName]
            .filter { !$0.isEmpty }
            .joined(separator: " ")
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
        guard let first = displayName.first else { return "#" }
        let title = String(first).uppercased()
        return title.rangeOfCharacter(from: .letters) == nil ? "#" : title
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
                $0.displayName.localizedStandardCompare($1.displayName) == .orderedAscending
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
