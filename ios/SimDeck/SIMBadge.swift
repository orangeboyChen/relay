import SwiftUI

struct SIMBadge: View {
    let line: DeviceLineIdentity

    @ScaledMetric(relativeTo: .caption) private var height = 30.0

    var body: some View {
        Text(line.numericID, format: .number.grouping(.never))
            .font(.caption.bold().monospacedDigit())
            .foregroundStyle(.white)
            .lineLimit(1)
            .minimumScaleFactor(0.7)
            .frame(width: height * 0.86, height: height)
            .background(Color.gray, in: SIMCardShape())
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

private struct SIMCardShape: Shape {
    func path(in rect: CGRect) -> Path {
        let cut = min(rect.width, rect.height) * 0.28

        var path = Path()
        path.move(to: CGPoint(x: rect.minX, y: rect.minY))
        path.addLine(to: CGPoint(x: rect.maxX - cut, y: rect.minY))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.minY + cut))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.maxY))
        path.addLine(to: CGPoint(x: rect.minX, y: rect.maxY))
        path.closeSubpath()
        return path
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
