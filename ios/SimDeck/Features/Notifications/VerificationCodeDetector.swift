// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation

enum VerificationCodeDetector {
    private static let pattern = #"(?i)(?:验证码|verification\s*code|security\s*code|one[- ]?time\s*password|otp|code)\D{0,8}(\d{4,8})|\b(\d{4,8})\b"#
    private static let explicitPattern = #"(?i)(?:验证码|verification\s*code|security\s*code|one[- ]?time\s*password|otp)\D{0,8}(\d{4,8})"#

    static func code(in message: String) -> String? {
        code(in: message, pattern: pattern)
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
}
