import SwiftUI

struct CellularSignalIndicator: View {
    let bars: Int

    var body: some View {
        HStack(alignment: .bottom, spacing: 1.5) {
            ForEach(0..<4, id: \.self) { index in
                RoundedRectangle(cornerRadius: 1, style: .continuous)
                    .fill(index < clampedBars ? Color.primary : Color.secondary.opacity(0.22))
                    .frame(width: 3, height: CGFloat(index * 3 + 4))
            }
        }
        .frame(width: 16, height: 14, alignment: .bottom)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Cellular signal")
        .accessibilityValue("\(clampedBars) of 4 signal bars")
    }

    private var clampedBars: Int {
        min(max(bars, 0), 4)
    }
}

#if DEBUG
#Preview("Signal Levels") {
    VStack(alignment: .leading, spacing: 16) {
        ForEach(0...4, id: \.self) { bars in
            LabeledContent("\(bars) bars") {
                CellularSignalIndicator(bars: bars)
            }
        }
    }
    .padding()
}

#Preview("Signal Levels - Dark") {
    CellularSignalIndicator(bars: 3)
        .padding()
        .preferredColorScheme(.dark)
}
#endif
