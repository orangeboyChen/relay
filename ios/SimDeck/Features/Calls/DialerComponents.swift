import SwiftUI

struct LinePicker: View {
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

struct NumberEntry: View {
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

struct DialPad: View {
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

struct CallButton: View {
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
