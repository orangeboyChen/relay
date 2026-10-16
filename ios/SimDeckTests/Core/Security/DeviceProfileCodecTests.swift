import XCTest
@testable import SimDeck

final class DeviceProfileCodecTests: XCTestCase {
    private let keyData = Data(0..<32)

    func testProfileRoundTripsThroughAuthenticatedEncryption() throws {
        let key = try VaultKey(data: keyData)
        let profile = DeviceProfile(
            deviceName: "Living Room EC20",
            lineName: "Personal",
            phoneNumber: "13500000000"
        )

        let ciphertext = try DeviceProfileCodec.seal(profile, using: key)

        XCTAssertEqual(try DeviceProfileCodec.open(ciphertext, using: key), profile)
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("Living Room"))
        XCTAssertFalse(String(decoding: ciphertext, as: UTF8.self).contains("13500000000"))
    }

    func testWrongVaultKeyCannotOpenProfile() throws {
        let ciphertext = try DeviceProfileCodec.seal(
            DeviceProfile(deviceName: "Office", lineName: "Work", phoneNumber: "10086"),
            using: VaultKey(data: keyData)
        )

        XCTAssertThrowsError(
            try DeviceProfileCodec.open(
                ciphertext,
                using: VaultKey(data: Data(repeating: 9, count: VaultKey.length))
            )
        )
    }

    func testTamperedProfileCannotBeOpened() throws {
        let key = try VaultKey(data: keyData)
        var ciphertext = try DeviceProfileCodec.seal(.empty, using: key)
        ciphertext[ciphertext.index(before: ciphertext.endIndex)] ^= 0xff

        XCTAssertThrowsError(try DeviceProfileCodec.open(ciphertext, using: key))
    }

    func testEmptyPairingProfileDecodesAsBlankMetadata() throws {
        let key = try VaultKey(data: keyData)

        XCTAssertEqual(try DeviceProfileCodec.open(Data(), using: key), .empty)
    }
}
