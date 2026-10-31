// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct SIMBadge: View {
    let number: Int?
    let accessibilityLabel: Text
    private let baseSide: CGFloat
    private let color: Color

    @ScaledMetric(relativeTo: .caption) private var scale = 1.0

    init(line: DeviceLineIdentity, size: CGFloat = 18, color: Color = .secondary) {
        let presentation = SIMLabelPresentation(line: line, titleStyle: .identifier)
        number = presentation.number
        accessibilityLabel = Text(verbatim: presentation.accessibilityLabel)
        baseSide = size
        self.color = color
    }

    init(number: Int, size: CGFloat = 18, color: Color = .secondary) {
        let presentation = SIMLabelPresentation(
            number: number,
            displayName: nil,
            phoneNumber: nil,
            lineName: nil,
            titleStyle: .identifier
        )
        self.number = presentation.number
        accessibilityLabel = Text(verbatim: presentation.accessibilityLabel)
        baseSide = size
        self.color = color
    }

    init(snapshot: LineHistorySnapshot?, size: CGFloat = 18, color: Color = .secondary) {
        let presentation = SIMLabelPresentation(snapshot: snapshot, titleStyle: .identifier)
        number = presentation.number
        accessibilityLabel = Text(verbatim: presentation.accessibilityLabel)
        baseSide = size
        self.color = color
    }

    var body: some View {
        let badgeSide = min(baseSide * scale, baseSide + 4)

        SIMNumberSymbol(number: number, side: badgeSide, color: color)
            .frame(width: badgeSide, height: badgeSide)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(accessibilityLabel)
    }

    static func inlineText(number: Int?) -> Text {
        Text(Image(systemName: systemImageName(for: number)))
    }

    static func systemImageName(for number: Int?) -> String {
        guard let number else { return "questionmark.square.fill" }
        let candidate = "\(number).square.fill"
        return UIImage(systemName: candidate) == nil ? "simcard.fill" : candidate
    }
}

private struct SIMNumberSymbol: View {
    let number: Int?
    let side: CGFloat
    let color: Color

    var body: some View {
        Group {
            if let symbolName {
                Image(systemName: symbolName)
                    .font(.system(size: side))
            } else if let number {
                ZStack {
                    Image(systemName: "square.fill")
                        .font(.system(size: side))
                    Text(number, format: .number.grouping(.never))
                        .font(.system(size: side * 0.48, weight: .semibold, design: .rounded))
                        .foregroundStyle(.background)
                        .minimumScaleFactor(0.7)
                }
            } else {
                Image(systemName: "questionmark.square.fill")
                    .font(.system(size: side))
            }
        }
        .foregroundStyle(color)
    }

    private var symbolName: String? {
        guard let number else { return nil }
        let candidate = SIMBadge.systemImageName(for: number)
        return candidate == "simcard.fill" ? nil : candidate
    }
}

#if DEBUG
#Preview("SIM badges") {
    HStack(spacing: 16) {
        SIMBadge(line: PreviewFixtures.devices[0])
        SIMBadge(line: PreviewFixtures.devices[1])
        SIMBadge(
            line: DeviceLineIdentity(
                id: UUID(uuidString: "00000000-0000-0000-0000-000000000099")!,
                numericID: 99,
                deviceName: "Portable modem",
                lineName: "",
                phoneNumber: "",
                isOnline: true
            )
        )
    }
    .padding()
}

#Preview("SIM badges - Dark and Accessibility") {
    HStack(spacing: 20) {
        SIMBadge(line: PreviewFixtures.devices[0])
        SIMBadge(line: PreviewFixtures.devices[1])
    }
    .padding()
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#endif
