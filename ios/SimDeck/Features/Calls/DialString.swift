// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

enum DialString {
    static func normalized(_ input: String) -> String {
        input.filter { !$0.isWhitespace && !"-()".contains($0) }
    }

    static func isValid(_ input: String) -> Bool {
        let value = normalized(input)
        guard (2...32).contains(value.count) else { return false }

        for (index, character) in value.enumerated() {
            if character == "+" {
                guard index == 0 else { return false }
            } else if !character.isNumber && character != "*" && character != "#" {
                return false
            }
        }
        return true
    }
}
