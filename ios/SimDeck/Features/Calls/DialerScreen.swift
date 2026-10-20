// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct DialerScreen: View {
    let lines: [DeviceLineIdentity]
    let contacts: [ContactRecord]
    let externalRequest: ExternalDialRequest?
    let externalLineID: UUID?
    let callStarter: any CallStarting
    let onStarted: (UUID) -> Void
    let onExternalRequestConsumed: (UUID) -> Void

    @State private var selectedLineID: UUID?
    @State private var number = ""
    @State private var isCalling = false
    @State private var consumedRequestID: UUID?
    @State private var unknownContactNumber: DialerUnknownContactNumber?

    init(
        lines: [DeviceLineIdentity],
        contacts: [ContactRecord] = [],
        initialLineID: UUID?,
        initialNumber: String = "",
        externalRequest: ExternalDialRequest? = nil,
        externalLineID: UUID? = nil,
        callStarter: any CallStarting,
        onStarted: @escaping (UUID) -> Void,
        onExternalRequestConsumed: @escaping (UUID) -> Void = { _ in }
    ) {
        self.lines = lines
        self.contacts = contacts
        self.externalRequest = externalRequest
        self.externalLineID = externalLineID
        self.callStarter = callStarter
        self.onStarted = onStarted
        self.onExternalRequestConsumed = onExternalRequestConsumed
        _selectedLineID = State(initialValue: initialLineID)
        _number = State(initialValue: initialNumber)
    }

    var body: some View {
        VStack(spacing: 10) {
            Color.clear
                .frame(height: 4)
                .accessibilityHidden(true)

            NumberEntry(number: $number)

            ZStack(alignment: .top) {
                contactMatchContent
            }
            .frame(maxWidth: .infinity)
            .frame(height: 62, alignment: .top)

            DialPad(number: $number)

            HStack {
                Color.clear
                    .frame(width: 80, height: 80)

                Spacer()

                CallButton(isCalling: isCalling, isEnabled: selectedLine != nil) {
                    startCall()
                }

                Spacer()

                DialerDeleteButton(isVisible: !number.isEmpty) {
                    number = DialerNumberMutation.deletingLastCharacter(from: number)
                }
                .frame(width: 80, height: 80)
            }
            .padding(.top, 44)

            Spacer(minLength: 10)
        }
        .padding(.horizontal, 28)
        .padding(.bottom, 8)
        .safeAreaInset(edge: .top, spacing: 0) {
            HStack {
                LinePicker(lines: onlineLines, selection: $selectedLineID)
                Spacer()
                if !number.isEmpty {
                    Button {
                        unknownContactNumber = DialerUnknownContactNumber(number: number)
                    } label: {
                        Image(systemName: "person.crop.circle.badge.plus")
                            .font(.system(size: 20, weight: .medium))
                            .frame(width: 48, height: 48)
                            .contentShape(Circle())
                    }
                    .buttonStyle(.plain)
                    .glassEffect(.regular.interactive(), in: Circle())
                    .accessibilityLabel("Add Contact")
                    .transition(.opacity.combined(with: .scale(scale: 0.9)))
                }
            }
            .frame(height: 54)
            .padding(.horizontal, 16)
            .background(Color(uiColor: .systemBackground))
        }
        .animation(.snappy, value: number.isEmpty)
        .sheet(item: $unknownContactNumber) { item in
            SystemUnknownContactView(phoneNumber: item.number)
        }
        .toolbar(.hidden, for: .navigationBar)
        .onChange(of: externalRequest, initial: true) { _, request in
            apply(request)
        }
        .onChange(of: onlineLines.map(\.id), initial: true) { _, onlineLineIDs in
            guard !onlineLineIDs.contains(where: { $0 == selectedLineID }) else { return }
            selectedLineID = onlineLineIDs.count == 1 ? onlineLineIDs.first : nil
        }
    }

    private var onlineLines: [DeviceLineIdentity] {
        lines.filter(\.isOnline)
    }

    private var selectedLine: DeviceLineIdentity? {
        onlineLines.first(where: { $0.id == selectedLineID })
    }

    private var validationReason: LocalizedStringResource? {
        guard !onlineLines.isEmpty else {
            return "No SIM is online."
        }
        guard selectedLine != nil else {
            return "Select an online SIM."
        }
        guard !number.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return "Enter a phone number."
        }
        guard DialString.isValid(number) else {
            return "Enter a valid phone number."
        }
        return nil
    }

    @ViewBuilder
    private var contactMatchContent: some View {
        if let exactMatch {
            HStack(spacing: 6) {
                Text(exactMatch.contact.displayName)
                Text(exactMatch.phoneNumber.label)
                    .fontWeight(.semibold)
                    .foregroundStyle(.secondary)
            }
            .font(.body)
            .lineLimit(1)
            .frame(maxWidth: .infinity, minHeight: 44)
        } else if let firstMatch = partialMatches.first {
            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    Image(systemName: "person.crop.circle")
                    Text(firstMatch.contact.displayName)
                    Text(firstMatch.phoneNumber.displayValue)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 0)
                }
                .frame(minHeight: 30)

                if partialMatches.count > 1 {
                    Divider()
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass")
                        Text("\(partialMatches.count - 1) More Results")
                            .foregroundStyle(.secondary)
                        Spacer(minLength: 0)
                    }
                    .frame(minHeight: 30)
                }
            }
            .font(.subheadline)
            .padding(.horizontal, 12)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
    }

    private var exactMatch: DialerContactMatch? {
        DialerContactMatcher.exactMatch(number: number, contacts: contacts)
    }

    private var partialMatches: [DialerContactMatch] {
        guard exactMatch == nil else { return [] }
        return DialerContactMatcher.matches(number: number, contacts: contacts)
    }

    private func startCall() {
        guard !isCalling, let selectedLine, validationReason == nil else { return }
        let normalizedNumber = DialString.normalized(number)
        isCalling = true

        Task {
            do {
                try await callStarter.startCall(to: normalizedNumber, using: selectedLine.id)
                onStarted(selectedLine.id)
            } catch {
                #if DEBUG
                print("dialer startCall failed: \(String(reflecting: error))")
                #endif
            }
            isCalling = false
        }
    }

    private func apply(_ request: ExternalDialRequest?) {
        guard let request, request.id != consumedRequestID else { return }
        consumedRequestID = request.id
        number = request.number
        selectedLineID = externalLineID
        onExternalRequestConsumed(request.id)
    }
}

private struct DialerUnknownContactNumber: Identifiable {
    let id = UUID()
    let number: String
}

#if DEBUG
#Preview("Dialer") {
    DialerScreen(
        lines: PreviewFixtures.devices,
        contacts: PreviewContactFixtures.contacts,
        initialLineID: PreviewFixtures.devices[0].id,
        callStarter: CallKitCoordinator(
            transport: PreviewCallTransport(),
            integratesWithSystem: false
        ),
        onStarted: { _ in }
    )
}

#Preview("Dialer - No online SIM") {
    DialerScreen(
        lines: PreviewFixtures.devices.map {
            var line = $0
            line.isOnline = false
            return line
        },
        initialLineID: nil,
        callStarter: CallKitCoordinator(
            transport: PreviewCallTransport(),
            integratesWithSystem: false
        ),
        onStarted: { _ in }
    )
    .preferredColorScheme(.dark)
}

#Preview("Dialer - Prefilled External Call") {
    DialerScreen(
        lines: PreviewFixtures.devices,
        contacts: PreviewContactFixtures.contacts,
        initialLineID: PreviewFixtures.devices[0].id,
        initialNumber: "13500000000",
        callStarter: CallKitCoordinator(
            transport: PreviewCallTransport(),
            integratesWithSystem: false
        ),
        onStarted: { _ in }
    )
}
#endif
