// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct ConversationHeader: View {
    let contact: ContactRecord
    let usesAccessibilityLayout: Bool
    let transitionID: String
    let transitionNamespace: Namespace.ID
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: usesAccessibilityLayout ? -8 : -12) {
                PersonAvatar(
                    imageData: contact.thumbnailImageData,
                    displayName: contact.displayName,
                    organizationName: contact.organizationName,
                    size: usesAccessibilityLayout ? 48 : 56
                )
                .matchedTransitionSource(
                    id: transitionID,
                    in: transitionNamespace
                )
                .zIndex(1)

                contactIdentity
            }
            .frame(maxWidth: usesAccessibilityLayout ? 270 : 230)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(Text("Contact details for \(contact.displayName)"))
    }

    private var contactIdentity: some View {
        HStack(spacing: 3) {
            Text(contact.displayName)
                .font(
                    usesAccessibilityLayout
                        ? .subheadline.weight(.semibold)
                        : .footnote.weight(.semibold)
                )
                .lineLimit(1)

            Image(systemName: "chevron.right")
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.tertiary)
        }
        .foregroundStyle(.primary)
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .frame(minHeight: 28)
        .glassEffect(
            .regular
                .tint(Color(uiColor: .secondarySystemFill))
                .interactive(),
            in: Capsule()
        )
    }

}

struct MessageBubble: View {
    let message: MessageItem
    let lineSnapshot: LineHistorySnapshot?
    let showsLineBadge: Bool
    let canRetry: Bool
    let openAttachment: () -> Void
    let copyAction: () -> Void
    let copyVerificationCodeAction: () -> Void
    let forwardAction: () -> Void
    let detailsAction: () -> Void
    let retryAction: () -> Void
    let deleteAction: () -> Void
    @Bindable var timestampRevealModel: MessageTimestampRevealModel

    init(
        message: MessageItem,
        lineSnapshot: LineHistorySnapshot?,
        showsLineBadge: Bool = false,
        canRetry: Bool = true,
        openAttachment: @escaping () -> Void = {},
        copyAction: @escaping () -> Void = {},
        copyVerificationCodeAction: @escaping () -> Void = {},
        forwardAction: @escaping () -> Void = {},
        detailsAction: @escaping () -> Void = {},
        retryAction: @escaping () -> Void = {},
        deleteAction: @escaping () -> Void = {},
        timestampRevealModel: MessageTimestampRevealModel = MessageTimestampRevealModel()
    ) {
        self.message = message
        self.lineSnapshot = lineSnapshot
        self.showsLineBadge = showsLineBadge
        self.canRetry = canRetry
        self.openAttachment = openAttachment
        self.copyAction = copyAction
        self.copyVerificationCodeAction = copyVerificationCodeAction
        self.forwardAction = forwardAction
        self.detailsAction = detailsAction
        self.retryAction = retryAction
        self.deleteAction = deleteAction
        self.timestampRevealModel = timestampRevealModel
    }

    var body: some View {
        ZStack {
            swipeTimestamp

            HStack {
                if message.direction == .sent {
                    Spacer(minLength: 64)
                }

                VStack(alignment: message.direction == .sent ? .trailing : .leading, spacing: 2) {
                    if showsLineBadge {
                        lineBadge
                            .padding(
                                message.direction == .sent ? .trailing : .leading,
                                6
                            )
                    }
                    bubbleContent
                    messageMetadata
                }

                if message.direction == .received {
                    Spacer(minLength: 64)
                }
            }
        }
        .contentShape(.rect)
        .accessibilityAction(named: Text("Details")) {
            detailsAction()
        }
    }

    private var swipeTimestamp: some View {
        HStack {
            Spacer(minLength: 0)

            Text(
                message.sentAt,
                format: .dateTime.hour().minute()
            )
            .font(.caption2)
            .foregroundStyle(
                message.direction == .sent
                    ? AnyShapeStyle(.white)
                    : AnyShapeStyle(.secondary)
            )
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
            .opacity(timestampRevealModel.progress)
            .offset(
                x: MessageSwipeInteraction.maximumTimestampOffset
                    * (1 - timestampRevealModel.progress)
            )
            .accessibilityHidden(true)
        }
        .zIndex(1)
    }

    private var lineBadge: some View {
        SIMBadge(snapshot: lineSnapshot, size: 14)
            .accessibilityHidden(true)
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
                    .font(.body)
                    .foregroundStyle(message.direction == .sent ? .white : .primary)
                    .tint(message.direction == .sent ? .white : .blue)
                    .padding(.horizontal, message.attachment == nil ? 0 : 6)
            }
        }
        .padding(.horizontal, message.attachment == nil ? 14 : 8)
        .padding(.top, 8)
        .padding(.bottom, message.attachment != nil && message.body.isEmpty ? 8 : 9)
        .background {
            MessageBubbleBackground(
                direction: message.direction,
                color: bubbleColor
            )
        }
        .frame(maxWidth: 271, alignment: message.direction == .sent ? .trailing : .leading)
        .contentShape(.contextMenuPreview, bubbleShape)
        .contextMenu {
            MessageContextMenu(
                canCopy: !message.body.isEmpty,
                verificationCode: VerificationCodeDetector.code(in: message.body),
                canForward: !message.body.isEmpty,
                canRetry: canRetry && message.direction == .sent && message.status == .failed,
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
        Group {
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
            ? .green
            : Color(uiColor: .systemGray5)
    }

    private var bubbleShape: MessageBubbleShape {
        MessageBubbleShape(direction: message.direction)
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

private struct MessageBubbleBackground: View {
    let direction: MessageDirection
    let color: Color

    var body: some View {
        ZStack(alignment: direction == .sent ? .bottomTrailing : .bottomLeading) {
            MessageBubbleTailCorner(
                direction: direction,
                horizontalRadius: 16,
                verticalRadius: 14
            )
            .fill(color)
            .frame(width: 20, height: 19)
            .offset(x: direction == .sent ? 7 : -7, y: 1)

            MessageBubbleTailCorner(
                direction: direction,
                horizontalRadius: 10,
                verticalRadius: 10
            )
            .fill(.black)
            .frame(width: 25, height: 19)
            .offset(x: direction == .sent ? 25 : -25, y: 1)
            .blendMode(.destinationOut)

            RoundedRectangle(cornerRadius: 18, style: .circular)
                .fill(color)
        }
        .compositingGroup()
    }
}

private struct MessageBubbleTailCorner: Shape {
    let direction: MessageDirection
    let horizontalRadius: CGFloat
    let verticalRadius: CGFloat

    func path(in rect: CGRect) -> Path {
        var receivedPath = Path()
        receivedPath.move(to: CGPoint(x: rect.minX, y: rect.minY))
        receivedPath.addLine(to: CGPoint(x: rect.maxX, y: rect.minY))
        receivedPath.addLine(
            to: CGPoint(x: rect.maxX, y: rect.maxY - verticalRadius)
        )
        receivedPath.addCurve(
            to: CGPoint(x: rect.maxX - horizontalRadius, y: rect.maxY),
            control1: CGPoint(x: rect.maxX, y: rect.maxY - verticalRadius * 0.45),
            control2: CGPoint(x: rect.maxX - horizontalRadius * 0.45, y: rect.maxY)
        )
        receivedPath.addLine(to: CGPoint(x: rect.minX, y: rect.maxY))
        receivedPath.closeSubpath()

        guard direction == .sent else {
            return receivedPath
        }

        return receivedPath.applying(
            CGAffineTransform(
                a: -1,
                b: 0,
                c: 0,
                d: 1,
                tx: rect.minX + rect.maxX,
                ty: 0
            )
        )
    }
}

private struct MessageBubbleShape: Shape {
    let direction: MessageDirection

    func path(in rect: CGRect) -> Path {
        RoundedRectangle(cornerRadius: 18, style: .circular)
            .path(in: rect)
    }
}

struct MessageImage: View {
    let attachment: MessageImageAttachment

    @ViewBuilder
    var body: some View {
        if let data = attachment.data, let image = UIImage(data: data) {
            Image(uiImage: image)
                .resizable()
                .scaledToFill()
                .frame(width: 220, height: 165)
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        } else {
            Image(systemName: attachment.systemImageName ?? "photo")
                .font(.system(size: 52))
                .foregroundStyle(.white)
                .frame(width: 180, height: 120)
                .background(
                    .orange.gradient,
                    in: RoundedRectangle(cornerRadius: 12, style: .continuous)
                )
        }
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
    ConversationComponentsPreview()
        .padding()
}

private struct ConversationComponentsPreview: View {
    @Namespace private var contactTransitionNamespace

    var body: some View {
        VStack(spacing: 20) {
            ConversationHeader(
                contact: PreviewContactFixtures.contacts[0],
                usesAccessibilityLayout: false,
                transitionID: "preview-contact",
                transitionNamespace: contactTransitionNamespace,
                action: {}
            )

            ForEach(PreviewFixtures.conversations[0].messages) { message in
                MessageBubble(
                    message: message,
                    lineSnapshot: LineHistorySnapshot(line: PreviewFixtures.devices[0])
                )
            }
        }
    }
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

#Preview("Timestamp reveal") {
    VStack(spacing: 8) {
        ForEach(PreviewFixtures.conversations[0].messages.prefix(2)) { message in
            MessageBubble(
                message: message,
                lineSnapshot: LineHistorySnapshot(line: PreviewFixtures.devices[0]),
                timestampRevealModel: MessageTimestampRevealModel(progress: 1)
            )
        }
    }
    .padding()
}
#endif
