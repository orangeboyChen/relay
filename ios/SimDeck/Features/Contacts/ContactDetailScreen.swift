// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct ContactDetailScreen: View {
    let contact: ContactRecord
    let lines: [DeviceLineIdentity]
    let defaultMessageLineID: UUID?
    let callAction: (String) async -> Void
    let sendAction: (UUID, String, String) async throws -> Void

    @State private var pendingAction: ContactCommunicationAction?
    @State private var messageRecipient: ContactPhoneNumber?

    var body: some View {
        ScrollView {
            VStack(spacing: 18) {
                contactHeader
                communicationActions

                if !contact.phoneNumbers.isEmpty {
                    phoneNumbersCard
                }

                if !contact.organizationName.isEmpty,
                   contact.organizationName != contact.displayName {
                    informationCard
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
            .padding(.bottom, 32)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(uiColor: .systemGroupedBackground))
        .navigationTitle(contact.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            "Choose a phone number",
            isPresented: Binding(
                get: { pendingAction != nil },
                set: { if !$0 { pendingAction = nil } }
            ),
            titleVisibility: .visible
        ) {
            ForEach(contact.phoneNumbers) { phoneNumber in
                Button("\(phoneNumber.label) · \(phoneNumber.displayValue)") {
                    perform(pendingAction, with: phoneNumber)
                }
            }
            Button("Cancel", role: .cancel) {
                pendingAction = nil
            }
        }
        .sheet(item: $messageRecipient) { phoneNumber in
            NewMessageScreen(
                lines: lines,
                selectedLineID: preferredMessageLineID,
                recipient: phoneNumber.value,
                sendAction: sendAction
            )
        }
    }

    private var contactHeader: some View {
        VStack(spacing: 12) {
            ContactAvatar(contact: contact, size: 128)

            VStack(spacing: 4) {
                Text(contact.displayName)
                    .font(.title.bold())
                    .multilineTextAlignment(.center)

                if !contact.organizationName.isEmpty,
                   contact.organizationName != contact.displayName {
                    Text(contact.organizationName)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 24)
    }

    private var communicationActions: some View {
        HStack(spacing: 18) {
            ContactActionButton(title: "Message", systemImage: "message.fill") {
                choosePhoneNumber(for: .message)
            }
            ContactActionButton(title: "Call", systemImage: "phone.fill") {
                choosePhoneNumber(for: .call)
            }
        }
        .frame(maxWidth: .infinity)
        .disabled(contact.phoneNumbers.isEmpty)
    }

    private var phoneNumbersCard: some View {
        VStack(spacing: 0) {
            ForEach(Array(contact.phoneNumbers.enumerated()), id: \.element.id) { index, phoneNumber in
                HStack(spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(phoneNumber.label)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                        Text(phoneNumber.displayValue)
                            .font(.body)
                            .foregroundStyle(.primary)
                            .textSelection(.enabled)
                    }

                    Spacer(minLength: 8)

                    Button {
                        Task {
                            await callAction(phoneNumber.value)
                        }
                    } label: {
                        Image(systemName: "phone.fill")
                            .frame(width: 44, height: 44)
                    }
                    .accessibilityLabel("Call \(phoneNumber.displayValue) with Sim Deck")
                }
                .padding(.leading, 16)
                .padding(.trailing, 8)
                .padding(.vertical, 8)

                if index < contact.phoneNumbers.count - 1 {
                    Divider()
                        .padding(.leading, 16)
                }
            }
        }
        .background(.background, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    private var informationCard: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Company")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Text(contact.organizationName)
                .font(.body)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(.background, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    private var preferredMessageLineID: UUID? {
        if let defaultMessageLineID,
           lines.contains(where: { $0.id == defaultMessageLineID && $0.isOnline }) {
            return defaultMessageLineID
        }
        return lines.first(where: \.isOnline)?.id
    }

    private func choosePhoneNumber(for action: ContactCommunicationAction) {
        guard let firstNumber = contact.phoneNumbers.first else { return }
        if contact.phoneNumbers.count == 1 {
            perform(action, with: firstNumber)
        } else {
            pendingAction = action
        }
    }

    private func perform(
        _ action: ContactCommunicationAction?,
        with phoneNumber: ContactPhoneNumber
    ) {
        pendingAction = nil
        switch action {
        case .call:
            Task {
                await callAction(phoneNumber.value)
            }
        case .message:
            messageRecipient = phoneNumber
        case nil:
            break
        }
    }
}

private enum ContactCommunicationAction {
    case call
    case message
}

private struct ContactActionButton: View {
    let title: LocalizedStringKey
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: systemImage)
                    .font(.title2)
                    .foregroundStyle(.tint)
                    .frame(width: 64, height: 64)
                    .glassEffect(.regular.interactive(), in: Circle())
                Text(title)
                    .font(.caption)
            }
            .frame(minWidth: 82)
        }
        .buttonStyle(.plain)
    }
}

#if DEBUG
#Preview("Contact Detail") {
    NavigationStack {
        ContactDetailScreen(
            contact: PreviewContactFixtures.contacts[0],
            lines: PreviewFixtures.devices,
            defaultMessageLineID: PreviewFixtures.devices[0].id,
            callAction: { _ in },
            sendAction: { _, _, _ in }
        )
    }
}

#Preview("Contact Detail - Dark, Accessibility") {
    NavigationStack {
        ContactDetailScreen(
            contact: PreviewContactFixtures.contacts[1],
            lines: PreviewFixtures.devices,
            defaultMessageLineID: nil,
            callAction: { _ in },
            sendAction: { _, _, _ in }
        )
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility2)
}
#endif
