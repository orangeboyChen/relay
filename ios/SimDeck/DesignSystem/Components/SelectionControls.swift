// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct SelectionDoneButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "checkmark")
                .font(.body.weight(.semibold))
        }
        .buttonStyle(.glassProminent)
        .buttonBorderShape(.circle)
        .tint(.blue)
        .accessibilityLabel("Done")
    }
}

struct SelectionActionBar<Leading: View, Trailing: View>: View {
    let leading: Leading
    let trailing: Trailing

    init(
        @ViewBuilder leading: () -> Leading,
        @ViewBuilder trailing: () -> Trailing
    ) {
        self.leading = leading()
        self.trailing = trailing()
    }

    var body: some View {
        HStack {
            leading
            Spacer(minLength: 20)
            trailing
        }
        .controlSize(.large)
        .padding(.horizontal, 24)
        .padding(.bottom, 8)
    }
}
