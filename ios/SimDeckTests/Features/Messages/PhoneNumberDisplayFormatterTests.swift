import XCTest
@testable import SimDeck

final class PhoneNumberDisplayFormatterTests: XCTestCase {
    func testFormatsChineseMobileNumberLikeMessages() {
        XCTAssertEqual(
            PhoneNumberDisplayFormatter.string(from: "+8619945168684"),
            "+86 199 4516 8684"
        )
        XCTAssertEqual(
            PhoneNumberDisplayFormatter.string(from: "13500000000"),
            "135 0000 0000"
        )
    }

    func testFormatsChineseLandlineWithParenthesizedAreaCode() {
        XCTAssertEqual(
            PhoneNumberDisplayFormatter.string(from: "+861069100979"),
            "+86 (10) 6910 0979"
        )
        XCTAssertEqual(
            PhoneNumberDisplayFormatter.string(from: "+8675512345678"),
            "+86 (755) 1234 5678"
        )
    }

    func testFormatsNorthAmericanNumberWithParenthesizedAreaCode() {
        XCTAssertEqual(
            PhoneNumberDisplayFormatter.string(from: "+14155552671"),
            "+1 (415) 555-2671"
        )
    }

    func testPreservesServiceNumbersAndUnknownSenderIdentifiers() {
        XCTAssertEqual(PhoneNumberDisplayFormatter.string(from: "10086"), "10086")
        XCTAssertEqual(PhoneNumberDisplayFormatter.string(from: "106918195131998"), "106918195131998")
        XCTAssertEqual(PhoneNumberDisplayFormatter.string(from: "*100#"), "*100#")
    }

    func testConversationUsesFormattedNumberOnlyWhenContactNameIsMissing() {
        let numberConversation = MessageConversation(
            lineID: UUID(),
            remoteNumber: "+861069100979",
            contactName: nil,
            unreadCount: 0,
            messages: []
        )
        let contactConversation = MessageConversation(
            lineID: UUID(),
            remoteNumber: "+861069100979",
            contactName: "Carrier",
            unreadCount: 0,
            messages: []
        )

        XCTAssertEqual(numberConversation.displayName, "+86 (10) 6910 0979")
        XCTAssertEqual(contactConversation.displayName, "Carrier")
    }
}
