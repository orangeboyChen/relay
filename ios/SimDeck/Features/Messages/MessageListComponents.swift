// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct SIMFilterButton: View {
    let isSelected: Bool

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Image(systemName: "line.3.horizontal.decrease")
            .font(.system(size: 17, weight: .semibold))
            .frame(width: 28, height: 28)
            .foregroundStyle(
                isSelected
                    ? AnyShapeStyle(.white)
                    : AnyShapeStyle(.primary)
            )
            .padding(8)
            .background {
                Circle()
                    .fill(.tint)
                    .padding(4)
                    .opacity(isSelected ? 1 : 0)
            }
            .animation(reduceMotion ? nil : .snappy(duration: 0.24), value: isSelected)
            .glassEffect(.regular.interactive(), in: Circle())
    }
}

struct SIMFilterMenu: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    var body: some View {
        Menu {
            Picker("SIM", selection: $selection) {
                Label("All SIMs", systemImage: "simcard.2")
                    .tag(nil as UUID?)
                ForEach(lines) { line in
                    SIMLabel(line: line)
                        .tag(line.id as UUID?)
                }
            }
            .pickerStyle(.inline)
        } label: {
            SIMFilterButton(isSelected: selection != nil)
        }
        .buttonBorderShape(.circle)
        .accessibilityLabel("Filter by SIM")
        .accessibilityValue(selectionAccessibilityValue)
    }

    private var selectionAccessibilityValue: Text {
        guard let selection,
              let selectedLine = lines.first(where: { $0.id == selection }) else {
            return Text("All SIMs")
        }
        return Text(SIMLabelPresentation(line: selectedLine, titleStyle: .displayName).accessibilityLabel)
    }
}

struct ConversationRow: View {
    let conversation: MessageConversation
    let linePresentation: LineHistoryPresentation
    var contact: ContactRecord? = nil
    let referenceDate: Date
    var isMuted = false
    var isPinned = false
    var isEditing = false

    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @ScaledMetric(relativeTo: .subheadline) private var previewLineHeight = 18

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            PersonAvatar(
                imageData: contact?.thumbnailImageData,
                displayName: contact?.displayName ?? conversation.contactName,
                organizationName: contact?.organizationName,
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
            Text(displayName)
                .font(.headline)
                .lineLimit(1)

            Spacer(minLength: 8)

            if let lastMessage = conversation.lastMessage {
                if isMuted {
                    Image(systemName: "bell.slash.fill")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("Alerts hidden")
                }

                if isPinned {
                    Image(systemName: "pin.fill")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .accessibilityLabel("Pinned")
                }

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

                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
        }
    }

    private var accessibilityTitle: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(displayName)
                .font(.headline)
                .lineLimit(2)

            if let lastMessage = conversation.lastMessage {
                HStack(spacing: 4) {
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

                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.tertiary)
                        .accessibilityHidden(true)
                }
            }
        }
    }

    private var previewRow: some View {
        (
            SIMBadge.inlineText(number: linePresentation.snapshot?.numericID)
            + Text(" \(summary)")
        )
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .lineLimit(2)
            .frame(
                maxWidth: .infinity,
                minHeight: previewLineHeight * 2,
                alignment: .topLeading
            )
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

    private var displayName: String {
        let contactName = contact?.displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        if let contactName, !contactName.isEmpty {
            return contactName
        }
        return conversation.displayName
    }
}

enum MessageConversationPinStore {
    private static let key = "message-pinned-conversations-v1"

    static func isPinned(
        _ conversationID: ConversationID,
        defaults: UserDefaults = .standard
    ) -> Bool {
        identifiers(defaults: defaults).contains(identifier(for: conversationID))
    }

    static func setPinned(
        _ isPinned: Bool,
        conversationID: ConversationID,
        defaults: UserDefaults = .standard
    ) {
        var storedIdentifiers = identifiers(defaults: defaults)
        let conversationIdentifier = identifier(for: conversationID)
        if isPinned {
            storedIdentifiers.insert(conversationIdentifier)
        } else {
            storedIdentifiers.remove(conversationIdentifier)
        }
        defaults.set(Array(storedIdentifiers).sorted(), forKey: key)
    }

    private static func identifiers(defaults: UserDefaults) -> Set<String> {
        Set(defaults.stringArray(forKey: key) ?? [])
    }

    private static func identifier(for conversationID: ConversationID) -> String {
        "\(conversationID.deviceUUID.uuidString)|\(conversationID.remoteNumber)"
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
