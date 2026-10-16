import SwiftUI

struct PressFeedbackButtonStyle: ButtonStyle {
    var pressedScale = 0.94
    var pressedOpacity = 0.78

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(reduceMotion || !configuration.isPressed ? 1 : pressedScale)
            .opacity(configuration.isPressed ? pressedOpacity : 1)
            .animation(reduceMotion ? nil : .snappy, value: configuration.isPressed)
    }
}
