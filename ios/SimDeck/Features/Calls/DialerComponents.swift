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
                HStack(spacing: 8) {
                    SIMBadge(line: selectedLine)
                    Text(selectedLine.displayName)
                        .font(.subheadline)
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                        .minimumScaleFactor(0.75)
                    Image(systemName: "chevron.down")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.tertiary)
                }
                .padding(.horizontal, 12)
                .frame(minHeight: 36)
                .background(.quaternary, in: Capsule())
                .contentShape(Capsule())
            }
            .buttonStyle(.plain)
            .frame(minHeight: 44)
            .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
        } else if lines.isEmpty {
            Label("No online SIM", systemImage: "simcard.slash")
                .foregroundStyle(.secondary)
                .frame(minHeight: 44)
        } else {
            Menu {
                ForEach(lines) { line in
                    Button {
                        selection = line.id
                    } label: {
                        Text("SIM \(line.numericID) · \(line.displayName)")
                    }
                }
            } label: {
                Label("Select SIM", systemImage: "simcard")
                    .foregroundStyle(.primary)
                    .padding(.horizontal, 12)
                    .frame(minHeight: 36)
                    .background(.quaternary, in: Capsule())
                    .contentShape(Capsule())
            }
            .buttonStyle(.plain)
            .frame(minHeight: 44)
        }
    }
}

struct NumberEntry: View {
    @Binding var number: String

    var body: some View {
        ZStack(alignment: .trailing) {
            TextField("Phone number", text: $number, prompt: Text("Phone number").foregroundStyle(.tertiary))
                .font(.system(.largeTitle, design: .rounded).weight(.regular).monospacedDigit())
                .multilineTextAlignment(.center)
                .keyboardType(.phonePad)
                .textContentType(.telephoneNumber)
                .textInputAutocapitalization(.never)
                .lineLimit(1)
                .minimumScaleFactor(0.55)
                .padding(.horizontal, 48)

            if !number.isEmpty {
                Button {
                    number.removeLast()
                } label: {
                    Image(systemName: "delete.left.fill")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                        .frame(width: 44, height: 44)
                }
                .accessibilityLabel("Delete digit")
            }
        }
        .frame(height: 54)
    }
}

struct DialPad: View {
    @Binding var number: String

    private let keys = [
        DialPadKeyContent(symbol: "1", letters: ""),
        DialPadKeyContent(symbol: "2", letters: "ABC"),
        DialPadKeyContent(symbol: "3", letters: "DEF"),
        DialPadKeyContent(symbol: "4", letters: "GHI"),
        DialPadKeyContent(symbol: "5", letters: "JKL"),
        DialPadKeyContent(symbol: "6", letters: "MNO"),
        DialPadKeyContent(symbol: "7", letters: "PQRS"),
        DialPadKeyContent(symbol: "8", letters: "TUV"),
        DialPadKeyContent(symbol: "9", letters: "WXYZ"),
        DialPadKeyContent(symbol: "*", letters: ""),
        DialPadKeyContent(symbol: "0", letters: "+"),
        DialPadKeyContent(symbol: "#", letters: ""),
    ]

    var body: some View {
        LazyVGrid(
            columns: Array(repeating: GridItem(.flexible(), spacing: 20), count: 3),
            spacing: 14
        ) {
            ForEach(keys, id: \.self) { key in
                DialPadKey(content: key) { press in
                    guard let input = DialPadInput.output(for: key.symbol, press: press) else {
                        return
                    }
                    number.append(contentsOf: input)
                }
            }
        }
        .frame(maxWidth: 318)
    }
}

enum DialPadPress: Equatable {
    case tap
    case longPress
}

enum DialPadInput {
    static func output(for symbol: String, press: DialPadPress) -> String? {
        switch press {
        case .tap:
            symbol
        case .longPress:
            symbol == "0" ? "+" : nil
        }
    }
}

private struct DialPadKeyContent: Hashable {
    let symbol: String
    let letters: String
}

private struct DialPadKey: View {
    let content: DialPadKeyContent
    let onPress: (DialPadPress) -> Void

    @State private var handledLongPress = false
    @ScaledMetric(relativeTo: .title) private var scaledSize = 72.0

    var body: some View {
        Button {
            if handledLongPress {
                handledLongPress = false
            } else {
                onPress(.tap)
            }
        } label: {
            VStack(spacing: -2) {
                Text(verbatim: content.symbol)
                    .font(.system(size: 32, weight: .regular, design: .rounded))
                    .foregroundStyle(.primary)
                Text(verbatim: content.letters)
                    .font(.system(size: 9, weight: .semibold, design: .rounded))
                    .tracking(content.letters.count > 1 ? 2 : 0)
                    .foregroundStyle(.primary)
                    .frame(height: 11)
                    .opacity(content.letters.isEmpty ? 0 : 1)
                    .accessibilityHidden(true)
            }
            .frame(width: min(scaledSize, 82), height: min(scaledSize, 82))
            .background(Color(uiColor: .systemGray5), in: Circle())
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .simultaneousGesture(
            LongPressGesture(minimumDuration: 0.5)
                .onEnded { _ in
                    handledLongPress = true
                    onPress(.longPress)
                },
            including: content.symbol == "0" ? .all : .none
        )
        .accessibilityLabel(accessibilityLabel)
        .accessibilityActions {
            if content.symbol == "0" {
                Button("Enter plus") {
                    onPress(.longPress)
                }
            }
        }
    }

    private var accessibilityLabel: Text {
        if content.symbol == "0" {
            Text("0, long press for plus")
        } else {
            Text(verbatim: content.symbol)
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
                        .font(.system(size: 28, weight: .semibold))
                }
            }
            .frame(width: 72, height: 72)
            .foregroundStyle(.white)
            .background(.green, in: Circle())
        }
        .disabled(!isEnabled || isCalling)
        .accessibilityLabel("Call")
    }
}
