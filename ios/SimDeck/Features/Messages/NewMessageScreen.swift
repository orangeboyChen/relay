// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct NewMessageScreen: View {
    let lines: [DeviceLineIdentity]
    let sendAction: (UUID, String, String) async throws -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @FocusState private var focusedField: Field?
    @State private var draft: NewMessageDraft
    @State private var isSending = false
    @State private var errorMessage: String?

    init(
        lines: [DeviceLineIdentity],
        selectedLineID: UUID? = nil,
        recipient: String = "",
        message: String = "",
        sendAction: @escaping (UUID, String, String) async throws -> Void = { _, _, _ in }
    ) {
        self.lines = lines
        self.sendAction = sendAction
        let initialLineID = selectedLineID ?? lines.first(where: \.isOnline)?.id
        _draft = State(
            initialValue: NewMessageDraft(
                selectedLineID: initialLineID,
                recipient: recipient,
                body: message
            )
        )
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                addressFields
                Divider()

                if onlineLines.isEmpty {
                    ContentUnavailableView(
                        "No online SIM",
                        systemImage: "simcard",
                        description: Text("Connect a SIM before starting a new message.")
                    )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    Color.clear
                        .contentShape(Rectangle())
                        .onTapGesture {
                            focusedField = nil
                        }
                }
            }
            .background(Color(uiColor: .systemBackground))
            .safeAreaInset(edge: .bottom, spacing: 0) {
                VStack(spacing: 0) {
                    if let errorMessage {
                        Label(errorMessage, systemImage: "exclamationmark.circle.fill")
                            .font(.footnote)
                            .foregroundStyle(.red)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                            .background(.bar)
                    }
                    MessageComposer(
                        draft: $draft.body,
                        isTextEntryEnabled: canEditMessage && !isSending,
                        canSend: draft.canSend(using: lines) && !isSending,
                        attachmentAction: {
                            errorMessage = String(localized: "Image attachments are not available yet.")
                        },
                        sendAction: send
                    )
                }
            }
            .navigationTitle("New Message")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
            .task {
                selectAvailableLineIfNeeded()
            }
            .onChange(of: lines) {
                selectAvailableLineIfNeeded()
            }
        }
    }

    @ViewBuilder
    private var addressFields: some View {
        if dynamicTypeSize.isAccessibilitySize {
            accessibilityAddressFields
        } else {
            compactAddressFields
        }
    }

    private var compactAddressFields: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                addressLabel("From:")
                linePicker
            }
            .padding(.leading, 16)
            .padding(.trailing, 8)
            .frame(minHeight: 52)

            Divider()
                .padding(.leading, 16)

            HStack(spacing: 10) {
                addressLabel("To:")
                recipientField
                recipientButton
            }
            .padding(.leading, 16)
            .padding(.trailing, 8)
            .frame(minHeight: 52)
        }
    }

    private var accessibilityAddressFields: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                Text("From:")
                    .foregroundStyle(.secondary)
                linePicker
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)

            Divider()
                .padding(.leading, 16)

            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text("To:")
                        .foregroundStyle(.secondary)
                    Spacer()
                    recipientButton
                }
                recipientField
                    .frame(minHeight: 44)
            }
            .padding(.leading, 16)
            .padding(.trailing, 8)
            .padding(.vertical, 8)
        }
    }

    private var recipientField: some View {
        TextField("Recipient", text: $draft.recipient)
            .focused($focusedField, equals: .recipient)
            .keyboardType(.phonePad)
            .textContentType(.telephoneNumber)
            .textInputAutocapitalization(.never)
            .disabled(draft.selectedLineID == nil)
            .accessibilityLabel("Recipient")
    }

    private var recipientButton: some View {
        Button {
            focusedField = .recipient
        } label: {
            Image(systemName: "plus.circle.fill")
                .font(.title2)
                .frame(width: 44, height: 44)
        }
        .disabled(draft.selectedLineID == nil)
        .accessibilityLabel("Add recipient")
    }

    private func addressLabel(_ title: LocalizedStringKey) -> some View {
        Text(title)
            .foregroundStyle(.secondary)
            .frame(minWidth: 44, alignment: .trailing)
    }

    @ViewBuilder
    private var linePicker: some View {
        if onlineLines.isEmpty {
            Label("No online SIM", systemImage: "simcard")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            Menu {
                ForEach(onlineLines) { line in
                    Button {
                        draft.selectedLineID = line.id
                        focusedField = .recipient
                    } label: {
                        HStack {
                            SIMLabel(line: line)
                            if draft.selectedLineID == line.id {
                                Image(systemName: "checkmark")
                            }
                        }
                    }
                }
            } label: {
                Group {
                    if dynamicTypeSize.isAccessibilitySize, let selectedLine {
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                SIMLabel(line: selectedLine, titleStyle: .identifier)
                                Spacer(minLength: 8)
                                linePickerIndicator
                            }
                            Text(selectedLine.displayName)
                                .foregroundStyle(.primary)
                                .lineLimit(1)
                                .minimumScaleFactor(0.8)
                        }
                    } else {
                        HStack(spacing: 8) {
                            if let selectedLine {
                                SIMLabel(line: selectedLine)
                                    .foregroundStyle(.primary)
                            } else {
                                Text("Choose a SIM")
                            }

                            Spacer(minLength: 8)
                            linePickerIndicator
                        }
                    }
                }
                .contentShape(Rectangle())
                .frame(minHeight: 44)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(linePickerAccessibilityLabel)
        }
    }

    private var onlineLines: [DeviceLineIdentity] {
        lines.filter(\.isOnline)
    }

    private var selectedLine: DeviceLineIdentity? {
        onlineLines.first(where: { $0.id == draft.selectedLineID })
    }

    private var canEditMessage: Bool {
        selectedLine != nil && draft.hasValidRecipient
    }

    private func selectAvailableLineIfNeeded() {
        guard selectedLine == nil else { return }
        draft.selectedLineID = onlineLines.first?.id
    }

    private var linePickerAccessibilityLabel: Text {
        if let selectedLine {
            Text("From SIM \(selectedLine.numericID), \(selectedLine.displayName)")
        } else {
            Text("Choose a SIM")
        }
    }

    private var linePickerIndicator: some View {
        Image(systemName: "chevron.up.chevron.down")
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
    }

    private func send() {
        guard draft.canSend(using: lines),
              let lineID = draft.selectedLineID,
              !isSending else { return }
        let recipient = draft.recipient
        let body = draft.body
        errorMessage = nil
        isSending = true
        Task {
            do {
                try await sendAction(lineID, recipient, body)
                dismiss()
            } catch {
                errorMessage = error.localizedDescription
                isSending = false
            }
        }
    }
}

private extension NewMessageScreen {
    enum Field: Hashable {
        case recipient
    }
}

#if DEBUG
#Preview("New message") {
    NewMessageScreen(lines: PreviewFixtures.devices)
}

#Preview("New message - Ready") {
    NewMessageScreen(
        lines: PreviewFixtures.devices,
        selectedLineID: PreviewFixtures.devices[0].id,
        recipient: "13800000000",
        message: "See you at seven."
    )
}

#Preview("New message - Offline") {
    NewMessageScreen(
        lines: PreviewFixtures.devices.map {
            var line = $0
            line.isOnline = false
            return line
        }
    )
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}

#Preview("New message - Simplified Chinese") {
    NewMessageScreen(lines: PreviewFixtures.devices)
        .environment(\.locale, Locale(identifier: "zh-Hans"))
}
#endif
