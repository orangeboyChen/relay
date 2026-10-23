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
            } else if let image = PersonAvatarImageRenderer.image(
                displayName: displayName,
                organizationName: organizationName,
                size: size,
                scale: UIScreen.main.scale
            ) {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
            } else {
                Color.clear
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
        PersonAvatar(displayName: "10086", size: 56)
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
