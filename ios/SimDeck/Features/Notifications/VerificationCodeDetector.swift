// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum VerificationCodeDetector {
    private static let explicitPattern = #"(?i)(?:验证码|verification\s*code|security\s*code|one[- ]?time\s*password|otp)\D{0,8}(\d{4,8})"#
    private static let standalonePattern = #"\b(\d{4,8})\b"#

    static func code(in message: String) -> String? {
        if let explicitCode = code(in: message, pattern: explicitPattern) {
            return explicitCode
        }

        guard let expression = try? NSRegularExpression(pattern: standalonePattern) else {
            return nil
        }
        let range = NSRange(message.startIndex..<message.endIndex, in: message)
        for match in expression.matches(in: message, range: range) {
            let candidateRange = match.range(at: 1)
            guard candidateRange.location != NSNotFound,
                  !isPartOfFormattedPhoneNumber(candidateRange, in: message),
                  let swiftRange = Range(candidateRange, in: message) else {
                continue
            }
            return String(message[swiftRange])
        }
        return nil
    }

    static func explicitCode(in message: String) -> String? {
        code(in: message, pattern: explicitPattern)
    }

    private static func code(in message: String, pattern: String) -> String? {
        guard let expression = try? NSRegularExpression(pattern: pattern) else { return nil }
        let range = NSRange(message.startIndex..<message.endIndex, in: message)
        for match in expression.matches(in: message, range: range) {
            for index in 1..<match.numberOfRanges {
                let candidateRange = match.range(at: index)
                guard candidateRange.location != NSNotFound,
                      let swiftRange = Range(candidateRange, in: message) else { continue }
                let candidate = String(message[swiftRange])
                if (4...8).contains(candidate.count) { return candidate }
            }
        }
        return nil
    }

    private static func isPartOfFormattedPhoneNumber(
        _ candidateRange: NSRange,
        in message: String
    ) -> Bool {
        let value = message as NSString
        var lowerBound = candidateRange.location
        var upperBound = NSMaxRange(candidateRange)

        while lowerBound > 0,
              isPhoneLikeCharacter(value.character(at: lowerBound - 1)) {
            lowerBound -= 1
        }
        while upperBound < value.length,
              isPhoneLikeCharacter(value.character(at: upperBound)) {
            upperBound += 1
        }

        let surroundingValue = value.substring(
            with: NSRange(location: lowerBound, length: upperBound - lowerBound)
        )
        let digitCount = surroundingValue.unicodeScalars.reduce(into: 0) { count, scalar in
            if CharacterSet.decimalDigits.contains(scalar) {
                count += 1
            }
        }
        return digitCount >= 10
    }

    private static func isPhoneLikeCharacter(_ character: unichar) -> Bool {
        guard let scalar = UnicodeScalar(character) else { return false }
        return CharacterSet.decimalDigits.contains(scalar)
            || CharacterSet.whitespacesAndNewlines.contains(scalar)
            || "+-()".unicodeScalars.contains(scalar)
    }
}
