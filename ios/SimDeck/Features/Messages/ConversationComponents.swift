// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct ConversationHeader: View {
    let conversation: MessageConversation
    let linePresentation: LineHistoryPresentation
    let alternatives: [MessageConversation]
    let lines: [DeviceLineIdentity]
    let usesAccessibilityLayout: Bool
    @Binding var selectedID: ConversationID

    var body: some View {
        Menu {
            ForEach(alternatives) { alternative in
                let alternativeLine = alternative.linePresentation(in: lines)
                Button(lineLabel(for: alternativeLine)) {
                    selectedID = alternative.id
                }
            }
        } label: {
            Group {
                if usesAccessibilityLayout {
                    VStack(spacing: 2) {
                        MessageContactAvatar(
                            contactName: conversation.contactName,
                            diameter: 48
                        )
                        contactNameLabel
                            .font(.subheadline.weight(.semibold))
                        HStack(spacing: 3) {
                            SIMBadge(snapshot: linePresentation.snapshot, size: 12)
                            Text(linePresentation.displayName)
                                .lineLimit(1)
                        }
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: 270)
                    .padding(.vertical, 4)
                } else {
                    VStack(spacing: 1) {
                        MessageContactAvatar(
                            contactName: conversation.contactName,
                            diameter: 42
                        )
                        .overlay(alignment: .bottomTrailing) {
                            SIMBadge(snapshot: linePresentation.snapshot, size: 10)
                                .offset(x: 3, y: 2)
                        }

                        contactNameLabel
                            .font(.caption2.weight(.medium))
                    }
                    .frame(maxWidth: 210)
                }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(
            Text(verbatim: accessibilityLabel)
        )
    }

    private func lineLabel(for presentation: LineHistoryPresentation) -> String {
        guard let numericID = presentation.snapshot?.numericID else {
            return String(localized: "Unavailable SIM")
        }
        return String(localized: "SIM \(numericID) · \(presentation.displayName)")
    }

    private var accessibilityLabel: String {
        guard let numericID = linePresentation.snapshot?.numericID else {
            return "\(conversation.displayName), \(String(localized: "Unavailable SIM"))"
        }
        return "\(conversation.displayName), SIM \(numericID), \(linePresentation.displayName)"
    }

    private var contactNameLabel: some View {
        HStack(spacing: 2) {
            Text(conversation.displayName)
                .lineLimit(1)

            Image(systemName: "chevron.down")
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.tertiary)
        }
        .foregroundStyle(.primary)
    }
}

struct MessageBubble: View {
    let message: MessageItem
    let lineSnapshot: LineHistorySnapshot?
    let canRetry: Bool
    let openAttachment: () -> Void
    let replyAction: () -> Void
    let copyAction: () -> Void
    let copyVerificationCodeAction: () -> Void
    let forwardAction: () -> Void
    let detailsAction: () -> Void
    let retryAction: () -> Void
    let deleteAction: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding private var timestampRevealState: MessageTimestampRevealState
    @State private var horizontalOffset: CGFloat = 0

    init(
        message: MessageItem,
        lineSnapshot: LineHistorySnapshot?,
        canRetry: Bool = true,
        openAttachment: @escaping () -> Void = {},
        replyAction: @escaping () -> Void = {},
        copyAction: @escaping () -> Void = {},
        copyVerificationCodeAction: @escaping () -> Void = {},
        forwardAction: @escaping () -> Void = {},
        detailsAction: @escaping () -> Void = {},
        retryAction: @escaping () -> Void = {},
        deleteAction: @escaping () -> Void = {},
        timestampRevealState: Binding<MessageTimestampRevealState> = .constant(.init())
    ) {
        self.message = message
        self.lineSnapshot = lineSnapshot
        self.canRetry = canRetry
        self.openAttachment = openAttachment
        self.replyAction = replyAction
        self.copyAction = copyAction
        self.copyVerificationCodeAction = copyVerificationCodeAction
        self.forwardAction = forwardAction
        self.detailsAction = detailsAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
        _timestampRevealState = timestampRevealState
    }

    var body: some View {
        ZStack {
            swipeAffordances

            HStack {
                if message.direction == .sent {
                    Spacer(minLength: 48)
                }

                VStack(alignment: message.direction == .sent ? .trailing : .leading, spacing: 2) {
                    bubbleContent
                    messageMetadata
                }

                if message.direction == .received {
                    Spacer(minLength: 48)
                }
            }
            .offset(x: messageOffset)
        }
        .contentShape(.rect)
        .gesture(
            MessageHorizontalSwipeGesture(
                onChanged: { horizontal, vertical in
                    let proposedOffset = MessageSwipeInteraction.offset(
                        horizontal: horizontal,
                        vertical: vertical
                    )
                    if proposedOffset < 0 {
                        horizontalOffset = 0
                        timestampRevealState.update(
                            horizontal: horizontal,
                            vertical: vertical
                        )
                    } else {
                        timestampRevealState.end()
                        horizontalOffset = proposedOffset
                    }
                },
                onEnded: { horizontal, vertical in
                    let shouldReply = MessageSwipeInteraction.shouldReply(
                        horizontal: horizontal,
                        vertical: vertical
                    )
                    withAnimation(reduceMotion ? nil : .snappy) {
                        horizontalOffset = 0
                        timestampRevealState.end()
                    }
                    if shouldReply {
                        replyAction()
                    }
                }
            )
        )
        .accessibilityAction(named: Text("Reply")) {
            replyAction()
        }
        .accessibilityAction(named: Text("Details")) {
            detailsAction()
        }
    }

    private var swipeAffordances: some View {
        HStack(spacing: 0) {
            Image(systemName: "arrowshape.turn.up.left.fill")
                .font(.body.weight(.semibold))
                .foregroundStyle(.blue)
                .frame(width: 44, height: 44)
                .scaleEffect(replyProgress)
                .opacity(replyProgress)
                .accessibilityHidden(true)

            Spacer(minLength: 0)

            Text(
                message.sentAt,
                format: .dateTime.hour().minute()
            )
            .font(.caption2)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
            .opacity(timestampRevealState.progress)
            .accessibilityHidden(true)
        }
    }

    private var messageOffset: CGFloat {
        horizontalOffset > 0 ? horizontalOffset : timestampRevealState.offset
    }

    private var replyProgress: CGFloat {
        min(max(horizontalOffset / MessageSwipeInteraction.replyThreshold, 0), 1)
    }

    private var bubbleContent: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let attachment = message.attachment {
                Button(action: openAttachment) {
                    MessageImage(attachment: attachment)
                }
                .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.98))
                .accessibilityLabel("Preview image")
            }
            if !message.body.isEmpty {
                Text(MessageLinkDetector.attributedString(from: message.body))
                    .foregroundStyle(message.direction == .sent ? .white : .primary)
                    .tint(message.direction == .sent ? .white : .blue)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(bubbleColor, in: bubbleShape)
        .contentShape(.contextMenuPreview, bubbleShape)
        .contextMenu {
            MessageContextMenu(
                canCopy: !message.body.isEmpty,
                verificationCode: VerificationCodeDetector.code(in: message.body),
                canForward: !message.body.isEmpty,
                canRetry: canRetry && message.direction == .sent && message.status == .failed,
                replyAction: replyAction,
                copyAction: copyAction,
                copyVerificationCodeAction: copyVerificationCodeAction,
                forwardAction: forwardAction,
                detailsAction: detailsAction,
                retryAction: retryAction,
                deleteAction: deleteAction
            )
        }
    }

    private var messageMetadata: some View {
        HStack(spacing: 5) {
            SIMBadge(snapshot: lineSnapshot, size: 14)
            deliveryStatusView
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .animation(.easeInOut(duration: 0.2), value: message.status)
    }

    @ViewBuilder
    private var deliveryStatusView: some View {
        if message.displaysDeliveryStatus, message.status == .failed {
            Button(action: retryAction) {
                HStack(spacing: 2) {
                    Image(systemName: "exclamationmark.circle.fill")
                        .accessibilityHidden(true)
                    Text("Not Delivered")
                }
                    .fontWeight(.semibold)
                    .foregroundStyle(.red)
                    .frame(minHeight: 44)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Not Delivered")
            .accessibilityHint("Double-tap to try sending this message again.")
            .transition(.opacity.combined(with: .scale(scale: 0.96)))
        } else if message.displaysDeliveryStatus {
            Text(deliveryStatus)
                .transition(.opacity)
        }
    }

    private var bubbleColor: Color {
        message.direction == .sent
            ? .blue
            : Color(uiColor: .secondarySystemBackground)
    }

    private var bubbleShape: UnevenRoundedRectangle {
        if message.direction == .sent {
            UnevenRoundedRectangle(
                topLeadingRadius: 18,
                bottomLeadingRadius: 18,
                bottomTrailingRadius: 4,
                topTrailingRadius: 18,
                style: .continuous
            )
        } else {
            UnevenRoundedRectangle(
                topLeadingRadius: 18,
                bottomLeadingRadius: 4,
                bottomTrailingRadius: 18,
                topTrailingRadius: 18,
                style: .continuous
            )
        }
    }

    private var deliveryStatus: LocalizedStringResource {
        switch message.status {
        case .sending: "Sending"
        case .sent: "Sent"
        case .delivered: "Delivered"
        case .failed: "Failed"
        }
    }
}

struct MessageImage: View {
    let attachment: MessageImageAttachment

    var body: some View {
        Image(systemName: attachment.systemImageName)
            .font(.system(size: 52))
            .foregroundStyle(.white)
            .frame(width: 180, height: 120)
            .background(.orange.gradient, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

struct AttachmentPreview: View {
    let attachment: MessageImageAttachment

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            MessageImage(attachment: attachment)
                .scaleEffect(1.5)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(.black)
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done") { dismiss() }
                    }
                }
        }
    }
}

#if DEBUG
#Preview("Conversation components") {
    VStack(spacing: 20) {
        ConversationHeader(
            conversation: PreviewFixtures.conversations[0],
            linePresentation: PreviewFixtures.conversations[0].linePresentation(
                in: PreviewFixtures.devices
            ),
            alternatives: PreviewFixtures.conversations,
            lines: PreviewFixtures.devices,
            usesAccessibilityLayout: false,
            selectedID: .constant(PreviewFixtures.conversations[0].id)
        )

        ForEach(PreviewFixtures.conversations[0].messages) { message in
            MessageBubble(
                message: message,
                lineSnapshot: LineHistorySnapshot(line: PreviewFixtures.devices[0])
            ) {}
        }
    }
    .padding()
}

#Preview("Failed message") {
    MessageBubble(
        message: MessageItem(
            id: UUID(uuidString: "20000000-0000-0000-0000-000000000099")!,
            direction: .sent,
            body: "Please try sending this again.",
            attachment: nil,
            sentAt: PreviewFixtures.now,
            status: .failed
        ),
        lineSnapshot: LineHistorySnapshot(line: PreviewFixtures.devices[0]),
        retryAction: {}
    )
    .padding()
}
#endif
