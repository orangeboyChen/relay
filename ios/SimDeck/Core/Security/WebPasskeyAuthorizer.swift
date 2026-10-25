import AuthenticationServices
import Foundation
import UIKit

struct WebPasskeySession: Sendable {
    let accessToken: String
    let refreshToken: String
    let accessExpiresAt: Date
    let refreshExpiresAt: Date
}

@MainActor
protocol WebPasskeyAuthorizing: AnyObject {
    func register(baseURL: URL, accessToken: String) async throws
    func signIn(baseURL: URL) async throws -> WebPasskeySession
}

enum WebPasskeyAuthorizationError: LocalizedError {
    case invalidURL
    case cancelled
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            String(localized: "The configured server address cannot open the passkey page.")
        case .cancelled:
            String(localized: "Passkey authorization was cancelled.")
        case let .failed(message):
            message
        }
    }
}

@MainActor
final class WebPasskeyAuthorizer: NSObject, WebPasskeyAuthorizing {
    private var session: ASWebAuthenticationSession?

    func register(baseURL: URL, accessToken: String) async throws {
        let callback = try await open(
            baseURL: baseURL,
            path: "passkey/register",
            parameters: ["access_token": accessToken]
        )
        let values = callbackValues(from: callback)
        guard values["status"] == "success" else {
            throw WebPasskeyAuthorizationError.failed(
                values["error"] ?? String(localized: "Passkey registration failed.")
            )
        }
    }

    func signIn(baseURL: URL) async throws -> WebPasskeySession {
        let callback = try await open(baseURL: baseURL, path: "passkey/login", parameters: [:])
        let values = callbackValues(from: callback)
        guard values["status"] == "success",
              let accessToken = values["access_token"], !accessToken.isEmpty,
              let refreshToken = values["refresh_token"], !refreshToken.isEmpty,
              let accessExpiresAt = Self.date(from: values["access_expires_at"]),
              let refreshExpiresAt = Self.date(from: values["refresh_expires_at"]) else {
            throw WebPasskeyAuthorizationError.failed(
                values["error"] ?? String(localized: "Passkey sign-in failed.")
            )
        }
        return WebPasskeySession(
            accessToken: accessToken,
            refreshToken: refreshToken,
            accessExpiresAt: accessExpiresAt,
            refreshExpiresAt: refreshExpiresAt
        )
    }

    private func open(baseURL: URL, path: String, parameters: [String: String]) async throws -> URL {
        let expectedState = UUID().uuidString
        let pageURL = try Self.authorizationURL(
            baseURL: baseURL,
            path: path,
            parameters: parameters,
            state: expectedState
        )

        return try await withCheckedThrowingContinuation { continuation in
            let authenticationSession = ASWebAuthenticationSession(
                url: pageURL,
                callbackURLScheme: "simdeck"
            ) { [weak self] callbackURL, error in
                self?.session = nil
                if let error {
                    if (error as? ASWebAuthenticationSessionError)?.code == .canceledLogin {
                        continuation.resume(throwing: WebPasskeyAuthorizationError.cancelled)
                    } else {
                        continuation.resume(throwing: error)
                    }
                    return
                }
                guard let callbackURL else {
                    continuation.resume(throwing: WebPasskeyAuthorizationError.cancelled)
                    return
                }
                guard self?.callbackValues(from: callbackURL)["state"] == expectedState else {
                    continuation.resume(throwing: WebPasskeyAuthorizationError.failed(
                        String(localized: "The passkey callback state is invalid.")
                    ))
                    return
                }
                continuation.resume(returning: callbackURL)
            }
            authenticationSession.presentationContextProvider = self
            authenticationSession.prefersEphemeralWebBrowserSession = false
            session = authenticationSession
            guard authenticationSession.start() else {
                session = nil
                continuation.resume(throwing: WebPasskeyAuthorizationError.failed(
                    String(localized: "Unable to open Safari for passkey authorization.")
                ))
                return
            }
        }
    }

    static func authorizationURL(
        baseURL: URL,
        path: String,
        parameters: [String: String],
        state: String
    ) throws -> URL {
        guard let basePageURL = URL(string: path, relativeTo: baseURL)?.absoluteURL else {
            throw WebPasskeyAuthorizationError.invalidURL
        }
        var fragment = URLComponents()
        var items = parameters
        items["callback"] = "simdeck://passkey"
        items["state"] = state
        fragment.queryItems = items
            .sorted { $0.key < $1.key }
            .map { URLQueryItem(name: $0.key, value: $0.value) }
        guard let encodedFragment = fragment.percentEncodedQuery,
              var pageComponents = URLComponents(
                  url: basePageURL,
                  resolvingAgainstBaseURL: false
              ) else {
            throw WebPasskeyAuthorizationError.invalidURL
        }
        pageComponents.fragment = encodedFragment
        guard let pageURL = pageComponents.url else {
            throw WebPasskeyAuthorizationError.invalidURL
        }
        return pageURL
    }

    private func callbackValues(from url: URL) -> [String: String] {
        guard let fragment = URLComponents(url: url, resolvingAgainstBaseURL: false)?.fragment,
              var components = URLComponents(string: "?\(fragment)") else {
            return [:]
        }
        components.query = fragment
        return Dictionary(
            uniqueKeysWithValues: (components.queryItems ?? []).compactMap { item in
                guard let value = item.value else { return nil }
                return (item.name, value)
            }
        )
    }

    private static func date(from value: String?) -> Date? {
        guard let value else { return nil }
        let formatter = ISO8601DateFormatter()
        return formatter.date(from: value)
    }
}

extension WebPasskeyAuthorizer: ASWebAuthenticationPresentationContextProviding {
    nonisolated func presentationAnchor(for _: ASWebAuthenticationSession) -> ASPresentationAnchor {
        MainActor.assumeIsolated {
            UIApplication.shared.connectedScenes
                .compactMap { $0 as? UIWindowScene }
                .flatMap(\.windows)
                .first(where: \.isKeyWindow)
                ?? ASPresentationAnchor()
        }
    }
}
