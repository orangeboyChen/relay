import SwiftUI

struct SIMFilterMenu: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    var body: some View {
        Menu {
            Button("All SIMs") {
                selection = nil
            }
            ForEach(lines) { line in
                Button("SIM \(line.numericID) · \(line.displayName)") {
                    selection = line.id
                }
            }
        } label: {
            Label(
                "Filter by SIM",
                systemImage: selection == nil
                    ? "line.3.horizontal.decrease.circle"
                    : "line.3.horizontal.decrease.circle.fill"
            )
        }
    }
}

struct ConversationRow: View {
    let conversation: MessageConversation
    let linePresentation: LineHistoryPresentation

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        HStack(alignment: .top, spacing: 11) {
            MessageContactAvatar(
                contactName: conversation.contactName
            )

            VStack(alignment: .leading, spacing: 5) {
                if dynamicTypeSize.isAccessibilitySize {
                    accessibilityTitle
                } else {
                    titleRow
                }
                previewRow
            }
            .padding(.top, 1)
        }
        .padding(.vertical, 6)
        .overlay(alignment: .leading) {
            unreadIndicator
                .offset(x: -15)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var unreadIndicator: some View {
        if conversation.unreadCount > 0 {
            Circle()
                .fill(.blue)
                .frame(width: 8, height: 8)
                .padding(.top, 21)
                .accessibilityLabel("\(conversation.unreadCount) unread messages")
        }
    }

    private var titleRow: some View {
        HStack(spacing: 8) {
            Text(conversation.displayName)
                .font(.body)
                .fontWeight(conversation.unreadCount > 0 ? .semibold : .regular)
                .lineLimit(1)

            Spacer(minLength: 8)

            if let lastMessage = conversation.lastMessage {
                Text(lastMessage.sentAt, format: .dateTime.hour().minute())
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }
        }
    }

    private var accessibilityTitle: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(conversation.displayName)
                .font(.body)
                .fontWeight(conversation.unreadCount > 0 ? .semibold : .regular)
                .lineLimit(2)

            if let lastMessage = conversation.lastMessage {
                Text(lastMessage.sentAt, format: .dateTime.hour().minute())
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }
        }
    }

    private var previewRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: 7) {
            SIMBadge(snapshot: linePresentation.snapshot)

            Text(summary)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var summary: String {
        guard let lastMessage = conversation.lastMessage else {
            return String(localized: "No messages yet")
        }
        if lastMessage.body.isEmpty, lastMessage.attachment != nil {
            return String(localized: "Image")
        }
        return lastMessage.body
    }
}

struct MessageContactAvatar: View {
    let contactName: String?
    var diameter: CGFloat = 46

    var body: some View {
        Group {
            if let monogram {
                ZStack {
                    Circle()
                        .fill(Color(uiColor: .tertiarySystemFill))
                    Text(monogram)
                        .font(.headline.weight(.medium))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .minimumScaleFactor(0.65)
                        .padding(8)
                }
            } else {
                Image(systemName: "person.crop.circle.fill")
                    .resizable()
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, Color(uiColor: .systemBlue).opacity(0.62))
            }
        }
        .frame(width: diameter, height: diameter)
        .accessibilityHidden(true)
    }

    private var monogram: String? {
        guard let contactName = contactName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !contactName.isEmpty else {
            return nil
        }
        let nameParts = contactName.split(whereSeparator: { $0.isWhitespace })
        let initials = nameParts.prefix(2).compactMap(\.first)
        return initials.isEmpty ? nil : String(initials).uppercased()
    }
}

#if DEBUG
#Preview("Message rows") {
    List {
        ForEach(PreviewFixtures.conversations) { conversation in
            ConversationRow(
                conversation: conversation,
                linePresentation: conversation.linePresentation(in: PreviewFixtures.devices)
            )
        }
    }
    .listStyle(.plain)
}

#Preview("Message rows - Dark Accessibility") {
    List {
        ForEach(PreviewFixtures.conversations) { conversation in
            ConversationRow(
                conversation: conversation,
                linePresentation: conversation.linePresentation(in: PreviewFixtures.devices)
            )
        }
    }
    .listStyle(.plain)
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
