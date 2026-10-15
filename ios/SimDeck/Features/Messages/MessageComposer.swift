import SwiftUI

struct MessageComposer: View {
    @Binding var draft: String

    let isTextEntryEnabled: Bool
    let canSendOverride: Bool?
    let attachmentAction: () -> Void
    let sendAction: () -> Void

    init(
        draft: Binding<String>,
        isTextEntryEnabled: Bool = true,
        canSend: Bool? = nil,
        attachmentAction: @escaping () -> Void = {},
        sendAction: @escaping () -> Void = {}
    ) {
        _draft = draft
        self.isTextEntryEnabled = isTextEntryEnabled
        canSendOverride = canSend
        self.attachmentAction = attachmentAction
        self.sendAction = sendAction
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 8) {
            Button(action: attachmentAction) {
                Image(systemName: "plus")
                    .font(.body.weight(.semibold))
                    .frame(width: 34, height: 34)
                    .background(.quaternary, in: Circle())
                    .frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .disabled(!isTextEntryEnabled)
            .accessibilityLabel("Add image")

            HStack(alignment: .bottom, spacing: 4) {
                TextField("Text Message", text: $draft, axis: .vertical)
                    .lineLimit(1...5)
                    .padding(.leading, 12)
                    .padding(.vertical, 8)
                    .disabled(!isTextEntryEnabled)

                Button(action: sendAction) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title)
                        .frame(width: 40, height: 40)
                }
                .disabled(!canSend)
                .accessibilityLabel("Send message")
            }
            .background(Color(uiColor: .secondarySystemBackground), in: Capsule())
            .overlay {
                Capsule()
                    .stroke(Color(uiColor: .separator), lineWidth: 0.5)
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(.bar)
    }

    private var canSend: Bool {
        canSendOverride
            ?? !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
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
#endif
