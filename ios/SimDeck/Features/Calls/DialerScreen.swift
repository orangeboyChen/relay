import SwiftUI

struct DialerScreen: View {
    let lines: [DeviceLineIdentity]
    let callStarter: any CallStarting
    let onStarted: (UUID) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var selectedLineID: UUID?
    @State private var number = ""
    @State private var isCalling = false
    @State private var errorMessage: LocalizedStringResource?

    init(
        lines: [DeviceLineIdentity],
        initialLineID: UUID?,
        callStarter: any CallStarting,
        onStarted: @escaping (UUID) -> Void
    ) {
        self.lines = lines
        self.callStarter = callStarter
        self.onStarted = onStarted
        _selectedLineID = State(initialValue: initialLineID)
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 18) {
                LinePicker(lines: onlineLines, selection: $selectedLineID)

                NumberEntry(number: $number)

                DialPad(number: $number)

                if let reason = validationReason {
                    Text(reason)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                } else if let errorMessage {
                    Text(errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                }

                CallButton(isCalling: isCalling, isEnabled: validationReason == nil) {
                    startCall()
                }
            }
            .padding(.horizontal, 24)
            .padding(.bottom, 16)
            .navigationTitle("Dial")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") {
                        dismiss()
                    }
                    .disabled(isCalling)
                }
            }
            .interactiveDismissDisabled(isCalling)
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

    private func startCall() {
        guard let selectedLine, validationReason == nil else { return }
        let normalizedNumber = DialString.normalized(number)
        isCalling = true
        errorMessage = nil

        Task {
            do {
                try await callStarter.startCall(to: normalizedNumber, using: selectedLine.id)
                onStarted(selectedLine.id)
                dismiss()
            } catch {
                errorMessage = "Could not start the call."
                isCalling = false
            }
        }
    }
}

#if DEBUG
#Preview("Dialer") {
    DialerScreen(
        lines: PreviewFixtures.devices,
        initialLineID: PreviewFixtures.devices[0].id,
        callStarter: PreviewCallStarter(),
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
        callStarter: PreviewCallStarter(),
        onStarted: { _ in }
    )
    .preferredColorScheme(.dark)
}
#endif
