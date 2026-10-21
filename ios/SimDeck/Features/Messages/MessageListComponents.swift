// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct MessageListBottomBar: View {
    @Binding var searchText: String
    let canCompose: Bool
    let composeAction: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .font(.body.weight(.medium))
                    .foregroundStyle(.secondary)

                TextField("Search", text: $searchText)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()

                Image(systemName: "mic.fill")
                    .font(.body)
                    .foregroundStyle(.primary)
                    .accessibilityHidden(true)
            }
            .padding(.horizontal, 14)
            .frame(height: 50)
            .glassEffect(.regular, in: Capsule())

            Button(action: composeAction) {
                Image(systemName: "square.and.pencil")
                    .font(.system(size: 20, weight: .medium))
                    .frame(width: 48, height: 48)
            }
            .buttonStyle(.glass)
            .buttonBorderShape(.circle)
            .disabled(!canCompose)
            .frame(width: 50, height: 50)
            .accessibilityLabel("New Message")
            .accessibilityHint(canCompose ? Text("") : Text("An online SIM is required."))
        }
        .padding(.leading, 28)
        .padding(.trailing, 15)
        .padding(.vertical, 6)
    }
}

struct SIMFilterMenu: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    var body: some View {
        Menu {
            Button {
                selection = nil
            } label: {
                if selection == nil {
                    Label("All SIMs", systemImage: "checkmark")
                } else {
                    Text("All SIMs")
                }
            }
            ForEach(lines) { line in
                Button {
                    selection = line.id
                } label: {
                    HStack {
                        SIMLabel(line: line)
                        if selection == line.id {
                            Image(systemName: "checkmark")
                        }
                    }
                }
            }
        } label: {
            Image(systemName: "line.3.horizontal")
                .font(.body.weight(.semibold))
                .frame(width: 44, height: 44)
                .contentShape(Circle())
        }
        .buttonStyle(.glass)
        .buttonBorderShape(.circle)
        .controlSize(.large)
        .accessibilityLabel("Filter by SIM")
    }
}

struct ConversationRow: View {
    let conversation: MessageConversation
    let linePresentation: LineHistoryPresentation
    let referenceDate: Date
    var isMuted = false
    var isEditing = false

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            PersonAvatar(
                displayName: conversation.contactName,
                size: 46
            )

            VStack(alignment: .leading, spacing: 4) {
                if dynamicTypeSize.isAccessibilitySize {
                    accessibilityTitle
                } else {
                    titleRow
                }
                previewRow
            }
            .padding(.top, 1)
        }
        .padding(.vertical, 11)
        .overlay(alignment: .leading) {
            unreadIndicator
                .offset(x: -18)
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var unreadIndicator: some View {
        if conversation.unreadCount > 0 {
            Circle()
                .fill(.blue)
                .frame(width: 10, height: 10)
                .accessibilityLabel("\(conversation.unreadCount) unread messages")
        }
    }

    private var titleRow: some View {
        HStack(spacing: 8) {
            Text(conversation.displayName)
                .font(.headline)
                .lineLimit(1)

            Spacer(minLength: 8)

            if let lastMessage = conversation.lastMessage {
                Text(
                    MessageTimestampFormatter.string(
                        from: lastMessage.sentAt,
                        relativeTo: referenceDate
                    )
                )
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }

            if isMuted {
                Image(systemName: "bell.slash.fill")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("Alerts hidden")
            }

            Image(systemName: "chevron.right")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.tertiary)
                .accessibilityHidden(true)
        }
    }

    private var accessibilityTitle: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(conversation.displayName)
                .font(.headline)
                .lineLimit(2)

            if let lastMessage = conversation.lastMessage {
                Text(
                    MessageTimestampFormatter.string(
                        from: lastMessage.sentAt,
                        relativeTo: referenceDate
                    )
                )
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize(horizontal: true, vertical: false)
            }
        }
    }

    private var previewRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: 7) {
            SIMBadge(snapshot: linePresentation.snapshot, size: 14)

            Text(summary)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(isEditing ? 1 : 2)
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

#if DEBUG
#Preview("Message rows") {
    List {
        ForEach(PreviewFixtures.conversations) { conversation in
            ConversationRow(
                conversation: conversation,
                linePresentation: conversation.linePresentation(in: PreviewFixtures.devices),
                referenceDate: .now
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
                linePresentation: conversation.linePresentation(in: PreviewFixtures.devices),
                referenceDate: .now
            )
        }
    }
    .listStyle(.plain)
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
