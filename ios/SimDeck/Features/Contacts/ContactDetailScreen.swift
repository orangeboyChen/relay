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
        ZStack {
            ContactDetailBackdrop()
                .ignoresSafeArea()

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
                .padding(.bottom, 120)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
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
        VStack(spacing: 10) {
            ContactAvatar(contact: contact, size: 240)
                .background(.ultraThinMaterial, in: Circle())
                .overlay {
                    Circle()
                        .stroke(.white.opacity(0.24), lineWidth: 1)
                }
                .shadow(color: .black.opacity(0.08), radius: 20, y: 10)

            VStack(spacing: 4) {
                if let defaultLine {
                    HStack(spacing: 6) {
                        Text("Default:")
                        SIMBadge(line: defaultLine, size: 16)
                        Text(defaultLine.displayName)
                        Image(systemName: "chevron.right")
                            .font(.caption.weight(.semibold))
                    }
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .accessibilityElement(children: .combine)
                }

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
        .padding(.top, 18)
    }

    private var communicationActions: some View {
        HStack(spacing: 12) {
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
            ContactActionButton(title: "Video", systemImage: "video.fill", isEnabled: false) {}
            ContactActionButton(title: "Mail", systemImage: "envelope.fill", isEnabled: false) {}
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
                .padding(.leading, 16)
                .padding(.trailing, 8)
                .padding(.vertical, 8)

                if index < contact.phoneNumbers.count - 1 {
                    Divider()
                        .padding(.leading, 16)
                }
            }
        }
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 26, style: .continuous))
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
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 26, style: .continuous))
    }

    private var defaultLine: DeviceLineIdentity? {
        if let defaultMessageLineID,
           let line = lines.first(where: { $0.id == defaultMessageLineID }) {
            return line
        }
        return lines.first(where: \.isOnline) ?? lines.first
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
                    .frame(width: 58, height: 58)
                    .glassEffect(.regular.interactive(), in: Circle())
                Text(title)
                    .font(.caption)
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.plain)
        .disabled(!isEnabled)
        .opacity(isEnabled ? 1 : 0.48)
    }
}

private struct ContactDetailBackdrop: View {
    var body: some View {
        LinearGradient(
            colors: [
                Color.blue.opacity(0.42),
                Color.indigo.opacity(0.34),
                Color(uiColor: .systemGroupedBackground).opacity(0.94),
            ],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
        .overlay(.ultraThinMaterial.opacity(0.28))
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
