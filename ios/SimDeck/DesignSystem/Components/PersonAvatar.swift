// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct PersonAvatar: View {
    let imageData: Data?
    let displayName: String?
    let organizationName: String?
    var size: CGFloat = 44

    init(
        imageData: Data? = nil,
        displayName: String?,
        organizationName: String? = nil,
        size: CGFloat = 44
    ) {
        self.imageData = imageData
        self.displayName = displayName
        self.organizationName = organizationName
        self.size = size
    }

    var body: some View {
        Group {
            if let imageData, let image = UIImage(data: imageData) {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
            } else {
                PersonAvatarFallbackView(
                    fallback: PersonAvatarFallback.resolve(
                        displayName: displayName,
                        organizationName: organizationName
                    ),
                    size: size
                )
            }
        }
        .frame(width: size, height: size)
        .background(Color(uiColor: .systemBackground))
        .clipShape(PersonAvatarShape(isOrganization: hasOrganizationName))
        .accessibilityHidden(true)
    }

    private var hasOrganizationName: Bool {
        guard let organizationName else { return false }
        return !organizationName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}

enum PersonAvatarFallback: Equatable {
    case organization
    case monogram(String)
    case placeholder

    static func resolve(
        displayName: String?,
        organizationName: String?
    ) -> PersonAvatarFallback {
        if let organizationName,
           !organizationName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return .organization
        }

        guard let displayName = displayName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !displayName.isEmpty else {
            return .placeholder
        }

        let parts = displayName.split(whereSeparator: \Character.isWhitespace)
        let monogram = parts.prefix(2).compactMap(\.first)
        guard !monogram.isEmpty else { return .placeholder }
        return .monogram(String(monogram).uppercased())
    }
}

private struct PersonAvatarFallbackView: View {
    let fallback: PersonAvatarFallback
    let size: CGFloat

    var body: some View {
        ZStack {
            switch fallback {
            case .organization:
                Rectangle()
                    .fill(avatarGradient)
                Image(systemName: "building.2.fill")
                    .font(.system(size: size * 0.43, weight: .medium))
                    .foregroundStyle(.white)
            case .monogram(let monogram):
                Color(uiColor: .systemGray5)
                Text(monogram)
                    .font(.system(size: size * 0.4, weight: .medium, design: .rounded))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.65)
                    .padding(size * 0.16)
            case .placeholder:
                Image(systemName: "person.crop.circle.fill")
                    .resizable()
                    .scaledToFit()
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(
                        .white,
                        avatarGradient
                    )
            }
        }
    }

    private var avatarGradient: LinearGradient {
        LinearGradient(
            colors: [
                Color(
                    red: 183.0 / 255.0,
                    green: 200.0 / 255.0,
                    blue: 225.0 / 255.0
                ),
                Color(
                    red: 143.0 / 255.0,
                    green: 154.0 / 255.0,
                    blue: 198.0 / 255.0
                ),
            ],
            startPoint: .top,
            endPoint: .bottom
        )
    }
}

private struct PersonAvatarShape: Shape {
    let isOrganization: Bool

    func path(in rect: CGRect) -> Path {
        if isOrganization {
            return RoundedRectangle(
                cornerRadius: rect.width * 0.24,
                style: .continuous
            ).path(in: rect)
        }
        return Circle().path(in: rect)
    }
}

#if DEBUG
#Preview("Person avatars") {
    HStack(spacing: 16) {
        PersonAvatar(displayName: "Zhang San", size: 56)
        PersonAvatar(displayName: "张三", size: 56)
        PersonAvatar(displayName: nil, organizationName: "Apple", size: 56)
        PersonAvatar(displayName: nil, size: 56)
    }
    .padding()
}

#Preview("Person avatars - Dark") {
    PersonAvatar(displayName: "Zhang San", size: 128)
        .padding()
        .preferredColorScheme(.dark)
}
#endif
