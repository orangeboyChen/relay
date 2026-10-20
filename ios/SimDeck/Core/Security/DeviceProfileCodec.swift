// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation

struct DeviceProfile: Codable, Equatable, Sendable {
    var deviceName: String
    var lineName: String
    var phoneNumber: String

    static let empty = DeviceProfile(deviceName: "", lineName: "", phoneNumber: "")

    enum CodingKeys: String, CodingKey {
        case deviceName = "device_name"
        case lineName = "line_name"
        case phoneNumber = "phone_number"
    }
}

enum DeviceProfileCodec {
    private static let authenticatedData = Data("sim-deck.device-profile.v1".utf8)

    static func seal(_ profile: DeviceProfile, using vaultKey: VaultKey) throws -> Data {
        let plaintext = try JSONEncoder().encode(profile)
        let box = try AES.GCM.seal(
            plaintext,
            using: SymmetricKey(data: vaultKey.data),
            authenticating: authenticatedData
        )
        guard let combined = box.combined else {
            throw KeyRecoveryError.invalidVaultKey
        }
        return combined
    }

    static func open(_ ciphertext: Data, using vaultKey: VaultKey) throws -> DeviceProfile {
        guard !ciphertext.isEmpty else { return .empty }
        let box = try AES.GCM.SealedBox(combined: ciphertext)
        let plaintext = try AES.GCM.open(
            box,
            using: SymmetricKey(data: vaultKey.data),
            authenticating: authenticatedData
        )
        return try JSONDecoder().decode(DeviceProfile.self, from: plaintext)
    }
}
