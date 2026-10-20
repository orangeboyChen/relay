import SwiftUI
import UIKit

struct ContactAvatar: View {
    let contact: ContactRecord
    var size: CGFloat = 44

    var body: some View {
        Group {
            if let imageData = contact.thumbnailImageData,
               let image = UIImage(data: imageData) {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
            } else if !contact.organizationName.isEmpty {
                ZStack {
                    LinearGradient(
                        colors: [.blue.opacity(0.36), .indigo.opacity(0.56)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                    Image(systemName: "building.2.fill")
                        .font(.system(size: size * 0.43, weight: .medium))
                        .foregroundStyle(.white)
                }
            } else if let initials = contact.initials {
                ZStack {
                    LinearGradient(
                        colors: [.blue.opacity(0.32), .indigo.opacity(0.52)],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                    Text(initials)
                        .font(.system(size: size * 0.4, weight: .medium, design: .rounded))
                        .foregroundStyle(.white)
                        .minimumScaleFactor(0.6)
                        .lineLimit(1)
                }
            } else {
                ZStack {
                    Color(uiColor: .systemGray5)
                    Image(systemName: "person.fill")
                        .font(.system(size: size * 0.48))
                        .foregroundStyle(Color(uiColor: .systemGray2))
                }
            }
        }
        .frame(width: size, height: size)
        .clipShape(Circle())
        .accessibilityHidden(true)
    }
}

#if DEBUG
#Preview("Contact Avatars") {
    HStack(spacing: 16) {
        ForEach(PreviewContactFixtures.contacts.prefix(3)) { contact in
            ContactAvatar(contact: contact, size: 56)
        }
    }
    .padding()
}
#endif
