// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI

enum MessageLinkDetector {
    static func attributedString(from body: String) -> AttributedString {
        var result = AttributedString(body)
        guard let detector = try? NSDataDetector(
            types: NSTextCheckingResult.CheckingType.link.rawValue
                | NSTextCheckingResult.CheckingType.phoneNumber.rawValue
        ) else { return result }

        let fullRange = NSRange(body.startIndex..<body.endIndex, in: body)
        for match in detector.matches(in: body, options: [], range: fullRange) {
            guard let target = targetURL(for: match),
                  let stringRange = Range(match.range, in: body),
                  let lowerBound = AttributedString.Index(stringRange.lowerBound, within: result),
                  let upperBound = AttributedString.Index(stringRange.upperBound, within: result) else {
                continue
            }
            result[lowerBound..<upperBound].link = target
            result[lowerBound..<upperBound].underlineStyle = .single
        }
        return result
    }

    private static func targetURL(for match: NSTextCheckingResult) -> URL? {
        if let url = match.url {
            return url
        }
        guard let phoneNumber = match.phoneNumber else { return nil }
        let callableNumber = phoneNumber.filter { $0.isNumber || $0 == "+" }
        return URL(string: "tel:\(callableNumber)")
    }
}
