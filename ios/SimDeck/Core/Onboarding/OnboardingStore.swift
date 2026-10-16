import Foundation

@MainActor
protocol OnboardingStoring: AnyObject {
    var isCompleted: Bool { get }
    func markCompleted()
}
@MainActor
final class UserDefaultsOnboardingStore: OnboardingStoring {
    private let defaults: UserDefaults
    private let key: String

    init(
        defaults: UserDefaults = .standard,
        key: String = "sim-deck.onboarding.completed.v1"
    ) {
        self.defaults = defaults
        self.key = key
    }

    var isCompleted: Bool {
        defaults.bool(forKey: key)
    }

    func markCompleted() {
        defaults.set(true, forKey: key)
    }
}
