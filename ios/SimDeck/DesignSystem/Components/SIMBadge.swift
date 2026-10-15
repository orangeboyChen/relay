import SwiftUI

struct SIMBadge: View {
    let line: DeviceLineIdentity
    private let baseSide: CGFloat

    @ScaledMetric(relativeTo: .caption) private var scale = 1.0

    init(line: DeviceLineIdentity, size: CGFloat = 18) {
        self.line = line
        baseSide = size
    }

    var body: some View {
        let badgeSide = min(baseSide * scale, baseSide + 4)

        Text(line.numericID, format: .number.grouping(.never))
            .font(.caption2.weight(.semibold).monospacedDigit())
            .foregroundStyle(.white)
            .lineLimit(1)
            .minimumScaleFactor(0.65)
            .frame(width: badgeSide, height: badgeSide)
            .background(
                Color(uiColor: .systemGray),
                in: RoundedRectangle(cornerRadius: badgeSide * 0.2, style: .continuous)
            )
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(accessibilityLabel)
    }

    private var accessibilityLabel: Text {
        if !line.phoneNumber.isEmpty {
            Text("SIM \(line.numericID), local number \(line.phoneNumber)")
        } else if !line.lineName.isEmpty {
            Text("SIM \(line.numericID), line \(line.lineName)")
        } else {
            Text("SIM \(line.numericID)")
        }
    }
}

#if DEBUG
#Preview("SIM badges") {
    HStack(spacing: 16) {
        SIMBadge(line: PreviewFixtures.devices[0])
        SIMBadge(line: PreviewFixtures.devices[1])
        SIMBadge(
            line: DeviceLineIdentity(
                id: UUID(uuidString: "00000000-0000-0000-0000-000000000099")!,
                numericID: 99,
                deviceName: "Portable modem",
                lineName: "",
                phoneNumber: "",
                isOnline: true
            )
        )
    }
    .padding()
}

#Preview("SIM badges - Dark and Accessibility") {
    HStack(spacing: 20) {
        SIMBadge(line: PreviewFixtures.devices[0])
        SIMBadge(line: PreviewFixtures.devices[1])
    }
    .padding()
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility5)
}
#endif
