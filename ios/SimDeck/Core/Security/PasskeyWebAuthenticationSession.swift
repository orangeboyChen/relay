import AuthenticationServices
import UIKit

@MainActor
final class PasskeyWebAuthenticationSession: NSObject, ASWebAuthenticationPresentationContextProviding {
    enum Failure: Error { case invalidURL; case canceled }

    private var session: ASWebAuthenticationSession?

    func authenticate(serverURL: URL, mode: String, accessToken: String? = nil) async throws -> URL {
        guard var components = URLComponents(
            url: serverURL.appending(path: "v1/auth/passkey"), resolvingAgainstBaseURL: false
        ) else { throw Failure.invalidURL }
        components.queryItems = [URLQueryItem(name: "mode", value: mode)]
        guard var url = components.url else { throw Failure.invalidURL }
        if let accessToken {
            guard let encodedToken = accessToken.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed),
                  let authenticatedURL = URL(string: url.absoluteString + "#token=" + encodedToken) else {
                throw Failure.invalidURL
            }
            url = authenticatedURL
        }
        return try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(
                url: url,
                callbackURLScheme: "simdeck"
            ) { [weak self] callback, error in
                self?.session = nil
                if let callback { continuation.resume(returning: callback) }
                else { continuation.resume(throwing: error ?? Failure.canceled) }
            }
            session.presentationContextProvider = self
            session.prefersEphemeralWebBrowserSession = true
            self.session = session
            session.start()
        }
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .flatMap(\.windows)
            .first(where: \.isKeyWindow) ?? ASPresentationAnchor()
    }
}
