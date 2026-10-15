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
