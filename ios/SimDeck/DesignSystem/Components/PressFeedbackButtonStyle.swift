// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct PressFeedbackButtonStyle: ButtonStyle {
    var pressedScale = 0.94
    var pressedOpacity = 0.78

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(reduceMotion || !configuration.isPressed ? 1 : pressedScale)
            .opacity(
                isEnabled
                    ? (configuration.isPressed ? pressedOpacity : 1)
                    : 0.35
            )
            .animation(reduceMotion ? nil : .snappy, value: configuration.isPressed)
            .animation(reduceMotion ? nil : .easeInOut(duration: 0.15), value: isEnabled)
    }
}
