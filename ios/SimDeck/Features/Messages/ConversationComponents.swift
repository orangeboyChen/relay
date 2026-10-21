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

    @ViewBuilder
    var body: some View {
        if usesAccessibilityLayout {
            lineMenu {
                VStack(spacing: 2) {
                    PersonAvatar(
                        displayName: conversation.contactName,
                        size: 48
                    )
                    contactNameLabel
                        .font(.subheadline.weight(.semibold))
                    SIMLabel(snapshot: linePresentation.snapshot, size: 12)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                }
                .frame(maxWidth: 270)
                .padding(.vertical, 4)
            }
        } else {
            VStack(spacing: 2) {
                PersonAvatar(
                    displayName: conversation.contactName,
                    size: 56
                )
                .overlay(alignment: .bottomTrailing) {
                    SIMBadge(snapshot: linePresentation.snapshot, size: 10)
                        .offset(x: 3, y: 2)
                }

                lineMenu {
                    contactNameLabel
                        .font(.footnote.weight(.semibold))
                        .padding(.horizontal, 10)
                        .frame(minHeight: 28)
                        .glassEffect(.regular.interactive(), in: Capsule())
                }
            }
            .frame(maxWidth: 230)
        }
    }

    private func lineMenu<Label: View>(
        @ViewBuilder label: () -> Label
    ) -> some View {
        Menu {
            ForEach(alternatives) { alternative in
                let alternativeLine = alternative.linePresentation(in: lines)
                Button {
                    selectedID = alternative.id
                } label: {
                    HStack {
                        SIMLabel(snapshot: alternativeLine.snapshot)
                        if alternative.id == selectedID {
                            Image(systemName: "checkmark")
                        }
                    }
                }
            }
        } label: {
            label()
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(
            Text(verbatim: accessibilityLabel)
        )
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
                    Spacer(minLength: 84)
                }

                VStack(alignment: message.direction == .sent ? .trailing : .leading, spacing: 2) {
                    bubbleContent
                    messageMetadata
                }

                if message.direction == .received {
                    Spacer(minLength: 84)
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
        .background(bubbleColor, in: bubbleBodyShape)
        .overlay(alignment: tailAlignment) {
            MessageBubbleTail(direction: message.direction)
                .fill(bubbleColor)
                .frame(width: 18, height: 20)
                .offset(x: message.direction == .received ? -9 : 9)
        }
        .frame(maxWidth: 271, alignment: message.direction == .sent ? .trailing : .leading)
        .contentShape(.contextMenuPreview, bubbleBodyShape)
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

    private var tailAlignment: Alignment {
        message.direction == .sent ? .bottomTrailing : .bottomLeading
    }

    private var bubbleBodyShape: UnevenRoundedRectangle {
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

private struct MessageBubbleTail: Shape {
    let direction: MessageDirection

    func path(in rect: CGRect) -> Path {
        var path = Path()
        if direction == .received {
            path.move(to: CGPoint(x: rect.maxX, y: rect.minY))
            path.addCurve(
                to: CGPoint(x: rect.minX, y: rect.maxY),
                control1: CGPoint(x: rect.maxX * 0.62, y: rect.height * 0.25),
                control2: CGPoint(x: rect.maxX * 0.5, y: rect.height * 0.8)
            )
            path.addCurve(
                to: CGPoint(x: rect.maxX, y: rect.height * 0.6),
                control1: CGPoint(x: rect.maxX * 0.34, y: rect.maxY),
                control2: CGPoint(x: rect.maxX * 0.78, y: rect.height * 0.85)
            )
        } else {
            path.move(to: CGPoint(x: rect.minX, y: rect.minY))
            path.addCurve(
                to: CGPoint(x: rect.maxX, y: rect.maxY),
                control1: CGPoint(x: rect.maxX * 0.38, y: rect.height * 0.25),
                control2: CGPoint(x: rect.maxX * 0.5, y: rect.height * 0.8)
            )
            path.addCurve(
                to: CGPoint(x: rect.minX, y: rect.height * 0.6),
                control1: CGPoint(x: rect.maxX * 0.66, y: rect.maxY),
                control2: CGPoint(x: rect.maxX * 0.22, y: rect.height * 0.85)
            )
        }
        path.closeSubpath()
        return path
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
