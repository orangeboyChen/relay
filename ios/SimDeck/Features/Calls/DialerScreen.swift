// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct DialerScreen: View {
    let lines: [DeviceLineIdentity]
    let externalRequest: ExternalDialRequest?
    let externalLineID: UUID?
    let callStarter: any CallStarting
    let onStarted: (UUID) -> Void
    let onExternalRequestConsumed: (UUID) -> Void

    @State private var selectedLineID: UUID?
    @State private var number = ""
    @State private var isCalling = false
    @State private var consumedRequestID: UUID?

    init(
        lines: [DeviceLineIdentity],
        initialLineID: UUID?,
        initialNumber: String = "",
        externalRequest: ExternalDialRequest? = nil,
        externalLineID: UUID? = nil,
        callStarter: any CallStarting,
        onStarted: @escaping (UUID) -> Void,
        onExternalRequestConsumed: @escaping (UUID) -> Void = { _ in }
    ) {
        self.lines = lines
        self.externalRequest = externalRequest
        self.externalLineID = externalLineID
        self.callStarter = callStarter
        self.onStarted = onStarted
        self.onExternalRequestConsumed = onExternalRequestConsumed
        _selectedLineID = State(initialValue: initialLineID)
        _number = State(initialValue: initialNumber)
    }

    var body: some View {
        VStack(spacing: 14) {
            Spacer(minLength: 72)

            NumberEntry(number: $number)

            DialPad(number: $number)

            if let reason = visibleValidationReason {
                Text(reason)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }

            CallButton(isCalling: isCalling, isEnabled: validationReason == nil) {
                startCall()
            }

            Spacer(minLength: 10)
        }
        .padding(.horizontal, 28)
        .padding(.bottom, 8)
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                LinePicker(lines: onlineLines, selection: $selectedLineID)
            }
        }
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

    private var visibleValidationReason: LocalizedStringResource? {
        guard number.isEmpty else { return validationReason }
        guard onlineLines.isEmpty else { return nil }
        return validationReason
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

#if DEBUG
#Preview("Dialer") {
    DialerScreen(
        lines: PreviewFixtures.devices,
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
        initialLineID: nil,
        initialNumber: "13500000000",
        callStarter: CallKitCoordinator(
            transport: PreviewCallTransport(),
            integratesWithSystem: false
        ),
        onStarted: { _ in }
    )
}
#endif
