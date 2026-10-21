// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct LinePicker: View {
    let lines: [DeviceLineIdentity]
    @Binding var selection: UUID?
    var isCompact = false

    var body: some View {
        if let selectedLine = lines.first(where: { $0.id == selection }) {
            Menu {
                ForEach(lines) { line in
                    Button {
                        selection = line.id
                    } label: {
                        if selection == line.id {
                            Label(
                                "SIM \(line.numericID) · \(line.displayName)",
                                systemImage: "checkmark"
                            )
                        } else {
                            Text("SIM \(line.numericID) · \(line.displayName)")
                        }
                    }
                }
            } label: {
                if isCompact {
                    HStack(spacing: 5) {
                        SIMBadge(line: selectedLine, size: 22)
                        Image(systemName: "chevron.up.chevron.down")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.secondary)
                    }
                    .frame(minWidth: 44, minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
                } else {
                    HStack(spacing: 8) {
                        SIMBadge(line: selectedLine, size: 22)
                        Text(selectedLine.displayName)
                            .font(.body)
                            .lineLimit(1)
                        Image(systemName: "chevron.down")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.horizontal, 12)
                    .frame(minHeight: 44)
                    .frame(minWidth: 198, alignment: .leading)
                    .contentShape(Capsule())
                    .glassEffect(.regular.interactive(), in: Capsule())
                }
            }
            .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
            .buttonStyle(.plain)
        } else if lines.isEmpty {
            Image(systemName: "simcard")
                .foregroundStyle(.secondary)
                .frame(width: 44, height: 44)
                .accessibilityLabel("No online SIM")
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
                Image(systemName: "simcard")
                    .foregroundStyle(.primary)
                    .frame(width: 44, height: 44)
                    .contentShape(.rect)
                    .accessibilityLabel("Select SIM")
            }
            .buttonStyle(.plain)
        }
    }
}

struct NumberEntry: View {
    @Binding var number: String

    var body: some View {
        DialerNumberTextField(text: $number)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
        .frame(height: 54)
    }
}

enum DialerNumberMutation {
    static func deletingLastCharacter(from number: String) -> String {
        guard !number.isEmpty else { return number }
        return String(number.dropLast())
    }
}

private struct DialerNumberTextField: UIViewRepresentable {
    @Binding var text: String

    func makeCoordinator() -> Coordinator {
        Coordinator(text: $text)
    }

    func makeUIView(context: Context) -> UITextField {
        let field = UITextField()
        field.delegate = context.coordinator
        field.addTarget(context.coordinator, action: #selector(Coordinator.textDidChange(_:)), for: .editingChanged)
        field.textAlignment = .center
        field.font = UIFont.monospacedDigitSystemFont(ofSize: 34, weight: .regular)
        field.textColor = .label
        field.tintColor = .tintColor
        field.adjustsFontSizeToFitWidth = true
        field.minimumFontSize = 20
        field.keyboardType = .phonePad
        field.textContentType = .telephoneNumber
        field.accessibilityLabel = String(localized: "Phone number")
        // The dial pad is the primary input surface; keep paste and editing while suppressing the system keyboard.
        field.inputView = UIView(frame: .zero)
        return field
    }

    func updateUIView(_ field: UITextField, context: Context) {
        let formattedText = PhoneNumberDisplayFormatter.string(from: text)
        if field.text != formattedText {
            field.text = formattedText
        }
    }

    final class Coordinator: NSObject, UITextFieldDelegate {
        private let textBinding: Binding<String>

        init(text: Binding<String>) {
            textBinding = text
        }

        @objc func textDidChange(_ field: UITextField) {
            textBinding.wrappedValue = DialString.normalized(field.text ?? "")
        }
    }
}

struct DialPad: View {
    @Binding var number: String
    var keySize: CGFloat = 80
    var rowSpacing: CGFloat = 18

    @State private var feedbackTrigger = 0

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
            columns: Array(repeating: GridItem(.flexible(), spacing: 16), count: 3),
            spacing: rowSpacing
        ) {
            ForEach(keys, id: \.self) { key in
                DialPadKey(content: key, size: keySize) { press in
                    guard let input = DialPadInput.output(for: key.symbol, press: press) else {
                        return
                    }
                    number.append(contentsOf: input)
                    feedbackTrigger += 1
                }
            }
        }
        .frame(maxWidth: 288)
        .sensoryFeedback(.selection, trigger: feedbackTrigger)
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
    let size: CGFloat
    let onPress: (DialPadPress) -> Void

    @State private var handledLongPress = false
    @ScaledMetric(relativeTo: .title) private var scaledSize = 80.0

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
            .frame(width: min(scaledSize, size), height: min(scaledSize, size))
            .glassEffect(.regular.interactive(), in: Circle())
            .contentShape(Circle())
        }
        .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
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
            .frame(width: 64, height: 64)
            .foregroundStyle(.white)
        }
        .buttonStyle(.glassProminent)
        .buttonBorderShape(.circle)
        .tint(.green)
        .disabled(!isEnabled || isCalling)
        .animation(.easeInOut(duration: 0.15), value: isCalling)
        .accessibilityLabel("Call")
    }
}

struct DialerDeleteButton: View {
    let isVisible: Bool
    let action: () -> Void

    @ViewBuilder
    var body: some View {
        if isVisible {
            Button(action: action) {
                Image(systemName: "delete.left.fill")
                    .font(.system(size: 24, weight: .regular))
                    .foregroundStyle(.primary)
                    .frame(width: 56, height: 56)
                    .contentShape(Rectangle())
            }
            .buttonStyle(PressFeedbackButtonStyle())
            .frame(width: 56, height: 56)
            .contentShape(Rectangle())
            .accessibilityLabel("Delete digit")
        } else {
            Color.clear
                .frame(width: 56, height: 56)
                .accessibilityHidden(true)
        }
    }
}
