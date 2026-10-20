// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct CallDetailScreen: View {
    let record: CallRecord
    let linePresentation: LineHistoryPresentation
    let lines: [DeviceLineIdentity]
    let defaultMessageLineID: UUID?
    let callAction: () -> Void
    let sendAction: (UUID, String, String) async throws -> Void

    @State private var isMessagePresented = false

    var body: some View {
        ZStack {
            CallDetailBackdrop()
                .ignoresSafeArea()

            ScrollView {
                VStack(spacing: 18) {
                    header
                    actionButtons
                    callHistoryCard
                    numberCard
                }
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 20)
                .padding(.bottom, 120)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .preferredColorScheme(.dark)
        .sheet(isPresented: $isMessagePresented) {
            NewMessageScreen(
                lines: lines,
                selectedLineID: preferredMessageLineID,
                recipient: record.remoteNumber,
                sendAction: sendAction
            )
        }
    }

    private var header: some View {
        VStack(spacing: 10) {
            ZStack {
                Circle()
                    .fill(.ultraThinMaterial)
                    .overlay {
                        Circle().stroke(.white.opacity(0.25), lineWidth: 1)
                    }
                if let initials {
                    Text(initials)
                        .font(.system(size: 84, weight: .medium, design: .rounded))
                } else {
                    Image(systemName: "person.fill")
                        .font(.system(size: 112))
                }
            }
            .foregroundStyle(.white)
            .frame(width: 240, height: 240)
            .shadow(color: .black.opacity(0.08), radius: 20, y: 10)

            VStack(spacing: 4) {
                HStack(spacing: 6) {
                    Text("SIM Line:")
                    SIMBadge(snapshot: linePresentation.snapshot, size: 16)
                    Text(linePresentation.displayName)
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.caption2.weight(.semibold))
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)

                Text(record.displayName)
                    .font(.title.bold())
                    .multilineTextAlignment(.center)

                if record.contactName != nil {
                    Text(record.remoteNumber)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }

                if let lineSubtitle {
                    Text(lineSubtitle)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.primary)
                }
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 18)
    }

    private var actionButtons: some View {
        HStack(spacing: 12) {
            detailAction(title: "Message", symbol: "message.fill") {
                isMessagePresented = true
            }
            detailAction(title: "Call", symbol: "phone.fill", action: callAction)
            detailAction(title: "Video", symbol: "video.fill", isEnabled: false) {}
            detailAction(title: "Mail", symbol: "envelope.fill", isEnabled: false) {}
        }
        .frame(maxWidth: .infinity)
        .tint(.white)
    }

    private var callHistoryCard: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Call History")
                    .font(.headline)
                Spacer()
                Image(systemName: "chevron.right")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            .padding(.bottom, 12)

            Divider()

            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(callKind)
                    if let duration = record.duration {
                        DurationText(seconds: duration)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Text(record.startedAt, format: .dateTime.day().month().hour().minute())
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            .padding(.top, 12)
        }
        .padding(16)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 26, style: .continuous))
    }

    private var numberCard: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Phone")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Text(record.remoteNumber)
                .textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 26, style: .continuous))
    }

    private func detailAction(
        title: LocalizedStringKey,
        symbol: String,
        isEnabled: Bool = true,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: symbol)
                    .font(.title2)
                    .foregroundStyle(.white.opacity(isEnabled ? 1 : 0.42))
                    .frame(width: 58, height: 58)
                    .glassEffect(.regular.interactive(), in: Circle())
                Text(title)
                    .font(.caption)
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.plain)
        .disabled(!isEnabled)
        .opacity(isEnabled ? 1 : 0.48)
    }

    private var preferredMessageLineID: UUID? {
        if let defaultMessageLineID,
           lines.contains(where: { $0.id == defaultMessageLineID && $0.isOnline }) {
            return defaultMessageLineID
        }
        if lines.contains(where: { $0.id == record.lineID && $0.isOnline }) {
            return record.lineID
        }
        return lines.first(where: \.isOnline)?.id
    }

    private var initials: String? {
        guard let name = record.contactName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !name.isEmpty else { return nil }
        let components = name.split(whereSeparator: \.isWhitespace)
        if components.isEmpty { return String(name.prefix(1)).uppercased() }
        return components.prefix(2).compactMap(\.first).map(String.init).joined().uppercased()
    }

    private var lineSubtitle: String? {
        guard let lineName = linePresentation.snapshot?.lineName
            .trimmingCharacters(in: .whitespacesAndNewlines),
            !lineName.isEmpty,
            lineName != linePresentation.displayName else {
            return nil
        }
        return lineName
    }

    private var callKind: LocalizedStringResource {
        switch record.result {
        case .missed: "Missed Call"
        case .rejected: "Rejected Call"
        case .failed: "Failed Call"
        case .answered where record.direction == .incoming: "Incoming Call"
        case .answered: "Outgoing Call"
        }
    }

}

private struct CallDetailBackdrop: View {
    var body: some View {
        LinearGradient(
            colors: [
                Color(red: 0.48, green: 0.52, blue: 0.68),
                Color(red: 0.29, green: 0.25, blue: 0.48),
                Color(red: 0.13, green: 0.07, blue: 0.28),
            ],
            startPoint: .top,
            endPoint: .bottom
        )
        .overlay(.ultraThinMaterial.opacity(0.12))
    }
}

#if DEBUG
#Preview("Call detail") {
    NavigationStack {
        CallDetailScreen(
            record: PreviewFixtures.calls[0],
            linePresentation: PreviewFixtures.calls[0].linePresentation(
                in: PreviewFixtures.devices
            ),
            lines: PreviewFixtures.devices,
            defaultMessageLineID: PreviewFixtures.devices.first?.id,
            callAction: {},
            sendAction: { _, _, _ in }
        )
    }
}

#Preview("Unknown caller detail") {
    NavigationStack {
        CallDetailScreen(
            record: PreviewFixtures.calls[1],
            linePresentation: PreviewFixtures.calls[1].linePresentation(
                in: PreviewFixtures.devices
            ),
            lines: PreviewFixtures.devices,
            defaultMessageLineID: PreviewFixtures.devices.first?.id,
            callAction: {},
            sendAction: { _, _, _ in }
        )
    }
}
#endif
