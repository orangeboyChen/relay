// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import XCTest
@testable import SimDeck

final class VerificationCodeDetectorTests: XCTestCase {
    func testRecognizesChineseVerificationCode() {
        XCTAssertEqual(VerificationCodeDetector.code(in: "您的验证码是 482913，5分钟内有效"), "482913")
    }

    func testRecognizesEnglishOtpLabel() {
        XCTAssertEqual(VerificationCodeDetector.code(in: "Your OTP code: 7304"), "7304")
    }

    func testDoesNotTreatPhoneNumberAsCode() {
        XCTAssertNil(VerificationCodeDetector.code(in: "请联系 13800000000"))
    }

    func testDoesNotTreatFormattedPhoneNumberGroupAsCode() {
        XCTAssertNil(
            VerificationCodeDetector.code(
                in: "Menu: https://example.com · +86 135 0000 0000"
            )
        )
    }

    func testRecognizesStandaloneCodeWithoutAnExplicitLabel() {
        XCTAssertEqual(VerificationCodeDetector.code(in: "Use 482913 to sign in"), "482913")
    }

    func testAutoFillRequiresExplicitVerificationLabel() {
        XCTAssertEqual(VerificationCodeDetector.explicitCode(in: "验证码：482913"), "482913")
        XCTAssertNil(VerificationCodeDetector.explicitCode(in: "订单号 482913"))
    }
}
