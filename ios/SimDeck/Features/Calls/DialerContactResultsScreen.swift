// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI

struct DialerContactResultsScreen: View {
    @Binding var number: String

    let contacts: [ContactRecord]
    let onSelect: (DialerContactMatch) -> Void
    let onCall: (DialerContactMatch) -> Void
    let onClose: () -> Void

    @FocusState private var isSearchFocused: Bool
    @State private var isShowingAllResults = false
    @State private var matches: [DialerContactMatch] = []

    var body: some View {
        VStack(spacing: 0) {
            searchHeader
            results
        }
        .background(Color(uiColor: .systemBackground))
        .task {
            refreshMatches()
            isSearchFocused = true
        }
        .onChange(of: number) { _, _ in
            isShowingAllResults = false
            refreshMatches()
        }
        .onChange(of: contacts) { _, _ in
            refreshMatches()
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    private var searchHeader: some View {
        HStack(spacing: 12) {
            HStack(spacing: 10) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 20, weight: .medium))
                    .foregroundStyle(.secondary)

                TextField("Phone number", text: $number)
                    .focused($isSearchFocused)
                    .keyboardType(.phonePad)
                    .textContentType(.telephoneNumber)
                    .font(.title3)

                if !number.isEmpty {
                    Button {
                        number = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.system(size: 20))
                            .foregroundStyle(.secondary)
                            .frame(width: 32, height: 32)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Clear phone number")
                }
            }
            .padding(.leading, 16)
            .padding(.trailing, 8)
            .frame(height: 52)
            .glassEffect(.regular, in: Capsule())

            Button {
                onClose()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 20, weight: .semibold))
                    .frame(width: 52, height: 52)
            }
            .buttonStyle(.plain)
            .glassEffect(.regular.interactive(), in: Circle())
            .accessibilityLabel("Close")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private var results: some View {
        ScrollView {
            LazyVStack(spacing: 0) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Phone Numbers")
                        .font(.title2.bold())
                    Spacer()
                    if matches.count > initialResultLimit, !isShowingAllResults {
                        Button("See All") {
                            isShowingAllResults = true
                        }
                        .font(.title3)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.top, 20)
                .padding(.bottom, 12)

                if visibleMatches.isEmpty {
                    ContentUnavailableView.search(text: number)
                        .padding(.top, 32)
                } else {
                    ForEach(visibleMatches) { match in
                        DialerContactResultRow(
                            match: match,
                            query: number,
                            onSelect: { onSelect(match) },
                            onCall: { onCall(match) }
                        )

                        if match.id != visibleMatches.last?.id {
                            Divider()
                                .padding(.leading, 88)
                                .padding(.trailing, 20)
                        }
                    }
                }
            }
        }
        .scrollDismissesKeyboard(.interactively)
    }

    private let initialResultLimit = 5

    private var visibleMatches: [DialerContactMatch] {
        if isShowingAllResults {
            return matches
        }
        return Array(matches.prefix(initialResultLimit))
    }

    private func refreshMatches() {
        matches = DialerContactMatcher.matches(number: number, contacts: contacts)
    }
}

private struct DialerContactResultRow: View {
    let match: DialerContactMatch
    let query: String
    let onSelect: () -> Void
    let onCall: () -> Void

    var body: some View {
        HStack(spacing: 14) {
            Button {
                onSelect()
            } label: {
                HStack(spacing: 14) {
                    PersonAvatar(
                        imageData: match.contact.thumbnailImageData,
                        displayName: match.contact.displayName,
                        organizationName: match.contact.organizationName,
                        size: 54
                    )

                    VStack(alignment: .leading, spacing: 2) {
                        Text(match.contact.displayName)
                            .font(.title3.weight(.semibold))
                            .foregroundStyle(.primary)
                        DialerHighlightedPhoneNumberText(
                            phoneNumber: match.phoneNumber.displayValue,
                            query: query
                        )
                        .font(.body)
                    }

                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(DialerContactMatchButtonStyle())

            Button {
                onCall()
            } label: {
                Image(systemName: "phone.fill")
                    .font(.system(size: 20, weight: .semibold))
                    .foregroundStyle(.tint)
                    .frame(width: 54, height: 54)
                    .background(Color(uiColor: .secondarySystemBackground), in: Circle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Call")
            .accessibilityValue(Text(verbatim: match.contact.displayName))
        }
        .padding(.leading, 20)
        .padding(.trailing, 20)
        .padding(.vertical, 10)
    }
}

struct DialerHighlightedPhoneNumberText: View {
    let phoneNumber: String
    let query: String

    var body: some View {
        if let highlighted = DialerContactMatcher.highlightedPhoneNumber(
            phoneNumber,
            matching: query
        ) {
            Text(attributedText(for: highlighted))
        } else {
            Text(verbatim: phoneNumber)
                .foregroundStyle(.secondary)
        }
    }

    private func attributedText(
        for highlighted: DialerHighlightedPhoneNumber
    ) -> AttributedString {
        var prefix = AttributedString(highlighted.prefix)
        prefix.foregroundColor = .secondary

        var match = AttributedString(highlighted.match)
        match.foregroundColor = .accentColor
        match.inlinePresentationIntent = .stronglyEmphasized

        var suffix = AttributedString(highlighted.suffix)
        suffix.foregroundColor = .secondary
        return prefix + match + suffix
    }
}

struct DialerContactMatchButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background(
                configuration.isPressed ? Color.secondary.opacity(0.14) : .clear,
                in: RoundedRectangle(cornerRadius: 10, style: .continuous)
            )
    }
}

#if DEBUG
#Preview("Dialer Results") {
    @Previewable @State var number = "135"

    DialerContactResultsScreen(
        number: $number,
        contacts: PreviewContactFixtures.contacts,
        onSelect: { _ in },
        onCall: { _ in },
        onClose: {}
    )
}
#endif
