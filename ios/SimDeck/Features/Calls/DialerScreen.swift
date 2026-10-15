import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var isDialerPresented = false

    var body: some View {
        CallHistoryScreen(
            state: model.callHistory,
            lines: model.devices,
            referenceDate: .now
        )
        .safeAreaInset(edge: .bottom) {
            DialerLauncherButton {
                isDialerPresented = true
            }
        }
        .sheet(isPresented: $isDialerPresented) {
            DialerScreen(
                lines: model.devices,
                initialLineID: preferredLineID,
                callStarter: model.callStarter
            ) { lineID in
                model.lastSuccessfulDialLineID = lineID
            }
            .presentationDetents([.large])
        }
    }

    private var preferredLineID: UUID? {
        if let lastID = model.lastSuccessfulDialLineID,
           model.devices.contains(where: { $0.id == lastID && $0.isOnline }) {
            return lastID
        }
        return model.devices.first(where: \.isOnline)?.id
    }
}

private struct DialerLauncherButton: View {
    let action: () -> Void

    var body: some View {
        HStack {
            Spacer()
            Button(action: action) {
                Image(systemName: "circle.grid.3x3.fill")
                    .font(.title2)
                    .frame(width: 56, height: 56)
                    .foregroundStyle(.white)
                    .background(.blue, in: Circle())
                    .shadow(radius: 5, y: 2)
            }
            .accessibilityLabel("Open dialer")
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
    }
}

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

private struct LinePicker: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?

    var body: some View {
        if let selectedLine = lines.first(where: { $0.id == selection }) {
            Menu {
                ForEach(lines) { line in
                    Button {
                        selection = line.id
                    } label: {
                        Text("SIM \(line.numericID) · \(line.displayName)")
                    }
                }
            } label: {
                HStack(spacing: 10) {
                    SIMBadge(line: selectedLine)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Outgoing line")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(selectedLine.displayName)
                            .foregroundStyle(.primary)
                    }
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down")
                        .foregroundStyle(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .frame(minHeight: 44)
        } else {
            Label("No online SIM", systemImage: "simcard.slash")
                .foregroundStyle(.secondary)
                .frame(minHeight: 44)
        }
    }
}

private struct NumberEntry: View {
    @Binding var number: String

    var body: some View {
        HStack(spacing: 8) {
            TextField("Phone number", text: $number)
                .font(.largeTitle.monospacedDigit())
                .multilineTextAlignment(.center)
                .keyboardType(.phonePad)
                .textContentType(.telephoneNumber)
                .textInputAutocapitalization(.never)

            Button {
                if !number.isEmpty {
                    number.removeLast()
                }
            } label: {
                Image(systemName: "delete.left.fill")
                    .frame(width: 44, height: 44)
            }
            .accessibilityLabel("Delete digit")
            .disabled(number.isEmpty)
        }
        .frame(minHeight: 60)
    }
}

private struct DialPad: View {
    @Binding var number: String

    private let keys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"]

    var body: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.flexible()), count: 3), spacing: 12) {
            ForEach(keys, id: \.self) { key in
                DialPadKey(symbol: key) {
                    number.append(contentsOf: key)
                } longPressAction: {
                    if key == "0" {
                        number.append("+")
                    }
                }
            }
        }
        .frame(maxWidth: 330)
    }
}

private struct DialPadKey: View {
    let symbol: String
    let action: () -> Void
    let longPressAction: () -> Void

    @State private var handledLongPress = false
    @ScaledMetric(relativeTo: .title) private var size = 64.0

    var body: some View {
        Button {
            if handledLongPress {
                handledLongPress = false
            } else {
                action()
            }
        } label: {
            VStack(spacing: 0) {
                Text(symbol)
                    .font(.title)
                    .foregroundStyle(.primary)
                if symbol == "0" {
                    Text(verbatim: "+")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            .frame(width: min(size, 78), height: min(size, 78))
            .background(.quaternary, in: Circle())
        }
        .buttonStyle(.plain)
        .onLongPressGesture(minimumDuration: 0.5) {
            handledLongPress = true
            longPressAction()
        }
        .accessibilityLabel(accessibilityLabel)
    }

    private var accessibilityLabel: Text {
        if symbol == "0" {
            Text("0, long press for plus")
        } else {
            Text(verbatim: symbol)
        }
    }
}

private struct CallButton: View {
    let isCalling: Bool
    let isEnabled: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Group {
                if isCalling {
                    ProgressView()
                        .tint(.white)
                } else {
                    Image(systemName: "phone.fill")
                        .font(.title2)
                }
            }
            .frame(width: 68, height: 68)
            .foregroundStyle(.white)
            .background(.green, in: Circle())
        }
        .disabled(!isEnabled || isCalling)
        .accessibilityLabel("Call")
    }
}

enum DialString {
    static func normalized(_ input: String) -> String {
        input.filter { !$0.isWhitespace && !"-()".contains($0) }
    }

    static func isValid(_ input: String) -> Bool {
        let value = normalized(input)
        guard (2...32).contains(value.count) else { return false }

        for (index, character) in value.enumerated() {
            if character == "+" {
                guard index == 0 else { return false }
            } else if !character.isNumber && character != "*" && character != "#" {
                return false
            }
        }
        return true
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
