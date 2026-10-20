// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import AuthenticationServices
import UIKit

struct PasskeyRegistrationChallenge: Equatable, Sendable {
    let relyingPartyID: String
    let challenge: Data
    let userID: Data
    let userName: String
}

struct PasskeyAssertionChallenge: Equatable, Sendable {
    let relyingPartyID: String
    let challenge: Data
}

struct PasskeyRegistrationCeremony: Equatable, Sendable {
    let id: String
    let challenge: PasskeyRegistrationChallenge
}

struct PasskeyAssertionCeremony: Equatable, Sendable {
    let id: String
    let challenge: PasskeyAssertionChallenge
}

struct PasskeyRegistrationCredential: Equatable, Sendable {
    let credentialID: Data
    let clientDataJSON: Data
    let attestationObject: Data
}

struct PasskeyAssertionCredential: Equatable, Sendable {
    let credentialID: Data
    let clientDataJSON: Data
    let authenticatorData: Data
    let signature: Data
    let userID: Data?
}

@MainActor
protocol PasskeyAuthorizing: AnyObject {
    func register(_ challenge: PasskeyRegistrationChallenge) async throws
        -> PasskeyRegistrationCredential
    func assert(_ challenge: PasskeyAssertionChallenge) async throws
        -> PasskeyAssertionCredential
}

enum PasskeyAuthorizationError: LocalizedError {
    case requestAlreadyActive
    case invalidCredential

    var errorDescription: String? {
        switch self {
        case .requestAlreadyActive:
            String(localized: "Another passkey request is already active.")
        case .invalidCredential:
            String(localized: "The system returned an invalid passkey credential.")
        }
    }
}

enum Base64URL {
    static func encode(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    static func decode(_ value: String) -> Data? {
        var base64 = value
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder != 0 {
            base64.append(String(repeating: "=", count: 4 - remainder))
        }
        return Data(base64Encoded: base64)
    }
}

@MainActor
final class NativePasskeyAuthorizer: NSObject, PasskeyAuthorizing {
    private enum Credential {
        case registration(PasskeyRegistrationCredential)
        case assertion(PasskeyAssertionCredential)
    }

    private var controller: ASAuthorizationController?
    private var continuation: CheckedContinuation<Credential, Error>?

    func register(
        _ challenge: PasskeyRegistrationChallenge
    ) async throws -> PasskeyRegistrationCredential {
        let provider = ASAuthorizationPlatformPublicKeyCredentialProvider(
            relyingPartyIdentifier: challenge.relyingPartyID
        )
        let request = provider.createCredentialRegistrationRequest(
            challenge: challenge.challenge,
            name: challenge.userName,
            userID: challenge.userID
        )
        let credential = try await authorize(request)
        guard case let .registration(registration) = credential else {
            throw PasskeyAuthorizationError.invalidCredential
        }
        return registration
    }

    func assert(
        _ challenge: PasskeyAssertionChallenge
    ) async throws -> PasskeyAssertionCredential {
        let provider = ASAuthorizationPlatformPublicKeyCredentialProvider(
            relyingPartyIdentifier: challenge.relyingPartyID
        )
        let request = provider.createCredentialAssertionRequest(challenge: challenge.challenge)
        let credential = try await authorize(request)
        guard case let .assertion(assertion) = credential else {
            throw PasskeyAuthorizationError.invalidCredential
        }
        return assertion
    }

    private func authorize(_ request: ASAuthorizationRequest) async throws -> Credential {
        guard continuation == nil else {
            throw PasskeyAuthorizationError.requestAlreadyActive
        }
        return try await withCheckedThrowingContinuation { continuation in
            let controller = ASAuthorizationController(authorizationRequests: [request])
            controller.delegate = self
            controller.presentationContextProvider = self
            self.controller = controller
            self.continuation = continuation
            controller.performRequests()
        }
    }

    private func complete(_ result: Result<Credential, Error>) {
        let continuation = continuation
        self.continuation = nil
        controller = nil
        continuation?.resume(with: result)
    }
}

extension NativePasskeyAuthorizer: ASAuthorizationControllerDelegate {
    func authorizationController(
        controller: ASAuthorizationController,
        didCompleteWithAuthorization authorization: ASAuthorization
    ) {
        if let registration = authorization.credential
            as? ASAuthorizationPlatformPublicKeyCredentialRegistration,
           let attestationObject = registration.rawAttestationObject {
            complete(.success(.registration(PasskeyRegistrationCredential(
                credentialID: registration.credentialID,
                clientDataJSON: registration.rawClientDataJSON,
                attestationObject: attestationObject
            ))))
            return
        }
        if let assertion = authorization.credential
            as? ASAuthorizationPlatformPublicKeyCredentialAssertion {
            complete(.success(.assertion(PasskeyAssertionCredential(
                credentialID: assertion.credentialID,
                clientDataJSON: assertion.rawClientDataJSON,
                authenticatorData: assertion.rawAuthenticatorData,
                signature: assertion.signature,
                userID: assertion.userID
            ))))
            return
        }
        complete(.failure(PasskeyAuthorizationError.invalidCredential))
    }

    func authorizationController(
        controller: ASAuthorizationController,
        didCompleteWithError error: Error
    ) {
        complete(.failure(error))
    }
}

extension NativePasskeyAuthorizer: ASAuthorizationControllerPresentationContextProviding {
    func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .flatMap(\.windows)
            .first(where: \.isKeyWindow) ?? ASPresentationAnchor()
    }
}
