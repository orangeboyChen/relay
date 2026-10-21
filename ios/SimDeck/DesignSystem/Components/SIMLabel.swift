// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct SIMLabel: View {
    enum TitleStyle: Equatable {
        case identifier
        case displayName
    }

    private let presentation: SIMLabelPresentation
    private let badgeSize: CGFloat

    init(
        line: DeviceLineIdentity,
        size: CGFloat = 18,
        titleStyle: TitleStyle = .displayName
    ) {
        presentation = SIMLabelPresentation(line: line, titleStyle: titleStyle)
        badgeSize = size
    }

    init(
        snapshot: LineHistorySnapshot?,
        size: CGFloat = 18,
        titleStyle: TitleStyle = .displayName
    ) {
        presentation = SIMLabelPresentation(snapshot: snapshot, titleStyle: titleStyle)
        badgeSize = size
    }

    init(
        number: Int,
        displayName: String,
        phoneNumber: String = "",
        lineName: String = "",
        size: CGFloat = 18,
        titleStyle: TitleStyle = .displayName
    ) {
        presentation = SIMLabelPresentation(
            number: number,
            displayName: displayName,
            phoneNumber: phoneNumber,
            lineName: lineName,
            titleStyle: titleStyle
        )
        badgeSize = size
    }

    var body: some View {
        HStack(spacing: 6) {
            if let number = presentation.number {
                SIMBadge(number: number, size: badgeSize)
                    .accessibilityHidden(true)
            } else {
                SIMBadge(snapshot: nil, size: badgeSize)
                    .accessibilityHidden(true)
            }

            Text(presentation.title)
                .lineLimit(1)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: presentation.accessibilityLabel))
    }
}

struct SIMLabelPresentation: Equatable {
    let number: Int?
    let title: String
    let accessibilityLabel: String

    init(line: DeviceLineIdentity, titleStyle: SIMLabel.TitleStyle) {
        self.init(
            number: line.numericID,
            displayName: line.displayName,
            phoneNumber: line.phoneNumber,
            lineName: line.lineName,
            titleStyle: titleStyle
        )
    }

    init(snapshot: LineHistorySnapshot?, titleStyle: SIMLabel.TitleStyle) {
        self.init(
            number: snapshot?.numericID,
            displayName: snapshot?.displayName,
            phoneNumber: snapshot?.phoneNumber,
            lineName: snapshot?.lineName,
            titleStyle: titleStyle
        )
    }

    init(
        number: Int?,
        displayName: String?,
        phoneNumber: String?,
        lineName: String?,
        titleStyle: SIMLabel.TitleStyle
    ) {
        self.number = number

        guard let number else {
            title = String(localized: "Unavailable SIM")
            accessibilityLabel = title
            return
        }

        switch titleStyle {
        case .identifier:
            title = String(localized: "SIM \(number)")
        case .displayName:
            let normalizedDisplayName = displayName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let normalizedPhoneNumber = phoneNumber?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let normalizedLineName = lineName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            if !normalizedDisplayName.isEmpty {
                title = normalizedDisplayName
            } else if !normalizedPhoneNumber.isEmpty {
                title = normalizedPhoneNumber
            } else if !normalizedLineName.isEmpty {
                title = normalizedLineName
            } else {
                title = String(localized: "SIM \(number)")
            }
        }

        let normalizedPhoneNumber = phoneNumber?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let normalizedLineName = lineName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if !normalizedPhoneNumber.isEmpty {
            accessibilityLabel = String(localized: "SIM \(number), local number \(normalizedPhoneNumber)")
        } else if !normalizedLineName.isEmpty {
            accessibilityLabel = String(localized: "SIM \(number), line \(normalizedLineName)")
        } else {
            accessibilityLabel = String(localized: "SIM \(number)")
        }
    }
}

#if DEBUG
#Preview("SIM labels") {
    VStack(alignment: .leading, spacing: 16) {
        SIMLabel(line: PreviewFixtures.devices[0])
        SIMLabel(line: PreviewFixtures.devices[1], titleStyle: .identifier)
        SIMLabel(
            number: 3,
            displayName: "",
            lineName: "Travel line",
            size: 22
        )
        SIMLabel(snapshot: nil)
    }
    .padding()
}

#Preview("SIM labels - Dark and Accessibility") {
    VStack(alignment: .leading, spacing: 20) {
        SIMLabel(line: PreviewFixtures.devices[0])
        SIMLabel(line: PreviewFixtures.devices[1], titleStyle: .identifier)
    }
    .padding()
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
