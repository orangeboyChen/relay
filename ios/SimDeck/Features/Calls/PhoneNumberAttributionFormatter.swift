// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import libPhoneNumberSwiftCore
import libPhoneNumberSwiftGeocoding

@MainActor
enum PhoneNumberAttributionFormatter {
    private static let phoneUtility = PhoneNumberUtility.shared
    private static let geocoder = PhoneNumberGeocoder.shared
    private static var cachedDescriptions: [String: String?] = [:]

    static func string(from input: String, locale: Locale = .current) -> String? {
        let cacheKey = "\(locale.identifier):\(input)"
        if let cachedDescription = cachedDescriptions[cacheKey] {
            return cachedDescription
        }

        let description = resolve(input, locale: locale)
        cachedDescriptions[cacheKey] = description
        return description
    }

    private static func resolve(_ input: String, locale: Locale) -> String? {
        let defaultRegion = defaultRegion(for: input, locale: locale)
        guard let phoneNumber = try? phoneUtility.parse(input, defaultRegion: defaultRegion),
              phoneUtility.isValidNumber(phoneNumber),
              let regionCode = phoneUtility.regionCode(for: phoneNumber) else {
            return nil
        }

        let languageCode = locale.language.languageCode?.identifier ?? "en"
        let locality = geocoder.description(
            forValidNumber: phoneNumber,
            languageCode: languageCode
        )?.trimmingCharacters(in: .whitespacesAndNewlines)
        let country = Locale(identifier: languageCode)
            .localizedString(forRegionCode: regionCode)?
            .trimmingCharacters(in: .whitespacesAndNewlines)

        guard let locality, !locality.isEmpty else {
            return country
        }
        guard let country, !country.isEmpty,
              locality.localizedCaseInsensitiveContains(country) == false else {
            return locality
        }
        return "\(locality), \(country)"
    }

    private static func defaultRegion(for input: String, locale: Locale) -> String? {
        let digits = input.filter(\.isNumber)
        if input.hasPrefix("+86")
            || digits.hasPrefix("0086")
            || (digits.count == 11 && digits.hasPrefix("1")) {
            return "CN"
        }
        return locale.region?.identifier
    }
}
