import SwiftUI

struct MessageComposer: View {
    @Binding var draft: String

    let isTextEntryEnabled: Bool
    let canSendOverride: Bool?
    let replyPreview: String?
    let attachmentAction: () -> Void
    let cancelReplyAction: () -> Void
    let sendAction: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    init(
        draft: Binding<String>,
        isTextEntryEnabled: Bool = true,
        canSend: Bool? = nil,
        replyPreview: String? = nil,
        attachmentAction: @escaping () -> Void = {},
        cancelReplyAction: @escaping () -> Void = {},
        sendAction: @escaping () -> Void = {}
    ) {
        _draft = draft
        self.isTextEntryEnabled = isTextEntryEnabled
        canSendOverride = canSend
        self.replyPreview = replyPreview
        self.attachmentAction = attachmentAction
        self.cancelReplyAction = cancelReplyAction
        self.sendAction = sendAction
    }

    var body: some View {
        VStack(spacing: 0) {
            if let replyPreview {
                MessageReplyBanner(
                    preview: replyPreview,
                    cancelAction: cancelReplyAction
                )
                .transition(
                    reduceMotion
                        ? .opacity
                        : .move(edge: .bottom).combined(with: .opacity)
                )
            }

            HStack(alignment: .bottom, spacing: 8) {
                Button(action: attachmentAction) {
                    Image(systemName: "plus")
                        .font(.system(size: 18, weight: .semibold))
                        .frame(width: 36, height: 36)
                        .background(.quaternary, in: Circle())
                        .frame(width: 44, height: 44, alignment: .bottom)
                }
                .buttonStyle(PressFeedbackButtonStyle())
                .disabled(!isTextEntryEnabled)
                .accessibilityLabel("Add image")

                HStack(alignment: .bottom, spacing: 4) {
                    TextField("Text Message", text: $draft, axis: .vertical)
                        .lineLimit(1...5)
                        .padding(.leading, 12)
                        .padding(.vertical, 7)
                        .disabled(!isTextEntryEnabled)

                    Button(action: sendAction) {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.system(size: 30))
                            .frame(width: 36, height: 36)
                    }
                    .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
                    .disabled(!canSend)
                    .accessibilityLabel("Send message")
                }
                .frame(minHeight: 36)
                .background(Color(uiColor: .secondarySystemBackground), in: Capsule())
                .overlay {
                    Capsule()
                        .stroke(Color(uiColor: .separator), lineWidth: 0.5)
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
        }
        .background(.bar)
        .animation(reduceMotion ? nil : .snappy, value: replyPreview)
    }

    private var canSend: Bool {
        canSendOverride
            ?? !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}

private struct MessageReplyBanner: View {
    let preview: String
    let cancelAction: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 1) {
                Text("Replying to")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.blue)
                Text(preview)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Button(action: cancelAction) {
                Image(systemName: "xmark.circle.fill")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Stop replying")
        }
        .padding(.leading, 71)
        .padding(.trailing, 8)
        .padding(.top, 6)
        .overlay(alignment: .leading) {
            RoundedRectangle(cornerRadius: 1, style: .continuous)
                .fill(.blue)
                .frame(width: 3)
                .padding(.leading, 58)
                .padding(.vertical, 4)
        }
    }
}

#if DEBUG
#Preview("Message composer") {
    @Previewable @State var draft = ""

    VStack {
        Spacer()
        MessageComposer(draft: $draft)
    }
}

#Preview("Message composer - Accessibility") {
    @Previewable @State var draft = "A longer message that wraps onto more than one line."

    VStack {
        Spacer()
        MessageComposer(draft: $draft)
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility3)
}

#Preview("Message composer - Reply") {
    @Previewable @State var draft = "Sounds good."

    VStack {
        Spacer()
        MessageComposer(
            draft: $draft,
            replyPreview: "Are you free for dinner?"
        )
    }
}
#endif
