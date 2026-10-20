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
        ScrollView {
            VStack(spacing: 18) {
                header
                actionButtons
                callHistoryCard
                numberCard
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
            .padding(.bottom, 36)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(background)
        .navigationTitle(record.displayName)
        .navigationBarTitleDisplayMode(.inline)
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
        VStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(.ultraThinMaterial)
                    .overlay {
                        Circle().stroke(.white.opacity(0.25), lineWidth: 1)
                    }
                if let initials {
                    Text(initials)
                        .font(.system(size: 64, weight: .medium, design: .rounded))
                } else {
                    Image(systemName: "person.fill")
                        .font(.system(size: 86))
                }
            }
            .foregroundStyle(.white)
            .frame(width: 184, height: 184)

            VStack(spacing: 4) {
                Text(record.displayName)
                    .font(.title.bold())
                    .multilineTextAlignment(.center)
                HStack(spacing: 6) {
                    SIMBadge(snapshot: linePresentation.snapshot, size: 16)
                    Text(linePresentation.displayName)
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 24)
    }

    private var actionButtons: some View {
        HStack(spacing: 18) {
            detailAction(title: "Message", symbol: "message.fill") {
                isMessagePresented = true
            }
            detailAction(title: "Call", symbol: "phone.fill", action: callAction)
        }
        .frame(maxWidth: .infinity)
    }

    private var callHistoryCard: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Call History")
                    .font(.headline)
                Spacer()
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
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
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
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 24, style: .continuous))
    }

    private func detailAction(
        title: LocalizedStringKey,
        symbol: String,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: symbol)
                    .font(.title2)
                    .frame(width: 64, height: 64)
                    .glassEffect(.regular.interactive(), in: Circle())
                Text(title)
                    .font(.caption)
            }
            .frame(minWidth: 82)
        }
        .buttonStyle(.plain)
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

    private var callKind: LocalizedStringResource {
        switch record.result {
        case .missed: "Missed Call"
        case .rejected: "Rejected Call"
        case .failed: "Failed Call"
        case .answered where record.direction == .incoming: "Incoming Call"
        case .answered: "Outgoing Call"
        }
    }

    private var background: LinearGradient {
        LinearGradient(
            colors: record.contactName == nil
                ? [.indigo.opacity(0.45), Color(uiColor: .systemGroupedBackground)]
                : [Color(uiColor: .systemGroupedBackground), Color(uiColor: .systemGroupedBackground)],
            startPoint: .top,
            endPoint: .bottom
        )
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
#endif
