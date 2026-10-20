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
            VStack(spacing: 0) {
                contactHeader

                if !contact.phoneNumbers.isEmpty {
                    communicationActions
                        .padding(.top, 22)

                    phoneNumbersCard
                        .padding(.top, 30)
                }

                if !contact.organizationName.isEmpty,
                   contact.organizationName != contact.displayName {
                    informationCard
                        .padding(.top, 16)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 16)
            .padding(.top, 28)
            .padding(.bottom, 120)
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
        VStack(spacing: 14) {
            ContactAvatar(contact: contact, size: 128)
                .shadow(color: .black.opacity(0.05), radius: 8, y: 4)

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
    }

    private var communicationActions: some View {
        HStack(spacing: 44) {
            ContactActionButton(
                title: "Message",
                systemImage: "message.fill",
                isEnabled: !contact.phoneNumbers.isEmpty
            ) {
                choosePhoneNumber(for: .message)
            }
            ContactActionButton(
                title: "Call",
                systemImage: "phone.fill",
                isEnabled: !contact.phoneNumbers.isEmpty
            ) {
                choosePhoneNumber(for: .call)
            }
        }
        .frame(maxWidth: .infinity)
    }

    private var phoneNumbersCard: some View {
        VStack(spacing: 0) {
            ForEach(Array(contact.phoneNumbers.enumerated()), id: \.element.id) { index, phoneNumber in
                Button {
                    Task {
                        await callAction(phoneNumber.value)
                    }
                } label: {
                    HStack(spacing: 12) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(phoneNumber.label)
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                            Text(phoneNumber.displayValue)
                                .font(.body)
                                .foregroundStyle(.primary)
                        }

                        Spacer(minLength: 8)

                        Image(systemName: "phone.fill")
                            .foregroundStyle(.tint)
                            .frame(width: 44, height: 44)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Call \(phoneNumber.displayValue) with Sim Deck")
                .contextMenu {
                    Button {
                        Task {
                            await callAction(phoneNumber.value)
                        }
                    } label: {
                        Label("Call", systemImage: "phone.fill")
                    }
                }
                .padding(.leading, 20)
                .padding(.trailing, 10)
                .padding(.vertical, 7)

                if index < contact.phoneNumbers.count - 1 {
                    Divider()
                        .padding(.leading, 20)
                }
            }
        }
        .background(
            Color(uiColor: .secondarySystemGroupedBackground),
            in: RoundedRectangle(cornerRadius: 24, style: .continuous)
        )
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
        .padding(20)
        .background(
            Color(uiColor: .secondarySystemGroupedBackground),
            in: RoundedRectangle(cornerRadius: 24, style: .continuous)
        )
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
    var isEnabled = true
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: systemImage)
                    .font(.title2)
                    .foregroundStyle(isEnabled ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                    .frame(width: 56, height: 56)
                    .glassEffect(.regular.interactive(), in: Circle())
                Text(title)
                    .font(.caption)
            }
            .frame(width: 74)
        }
        .buttonStyle(.plain)
        .disabled(!isEnabled)
        .opacity(isEnabled ? 1 : 0.48)
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
