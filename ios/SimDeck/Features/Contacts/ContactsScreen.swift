// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

struct ContactsScreen: View {
    @Bindable var model: AppModel
    @Binding private var searchText: String
    @State private var contactModel: ContactListModel
    private let startsSynchronization: Bool

    init(
        model: AppModel,
        searchText: Binding<String> = .constant(""),
        contactStore: (any ContactStoreProviding)? = nil,
        initialState: ContactListState = .loading,
        startsSynchronization: Bool = true
    ) {
        self.model = model
        _searchText = searchText
        _contactModel = State(
            initialValue: ContactListModel(
                store: contactStore ?? model.contactStore,
                state: initialState,
                searchText: searchText.wrappedValue
            )
        )
        self.startsSynchronization = startsSynchronization
    }

    var body: some View {
        Group {
            switch contactModel.state {
            case .permissionRequired:
                permissionRequiredContent
            case .permissionDenied:
                permissionDeniedContent
            case .loading:
                ProgressView("Loading Contacts…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .loaded(let sections):
                contactsContent(sections)
            case .failed:
                failedContent
            }
        }
        .task {
            guard startsSynchronization else { return }
            await contactModel.run()
        }
        .onChange(of: contactModel.state) { _, state in
            if case .loaded = state {
                model.refreshContactNames()
            }
        }
        .onChange(of: searchText, initial: true) { _, newValue in
            contactModel.searchText = newValue
        }
        .scrollEdgeEffectStyle(.soft, for: .all)
    }

    @ViewBuilder
    private func contactsContent(_ sections: [ContactSection]) -> some View {
        if sections.isEmpty {
            if contactModel.searchText.isEmpty {
                ContentUnavailableView(
                    "No Contacts",
                    systemImage: "person.2",
                    description: Text("Contacts saved in iOS appear here.")
                )
            } else {
                ContentUnavailableView.search(text: contactModel.searchText)
            }
        } else {
            ScrollViewReader { proxy in
                List {
                    ForEach(sections) { section in
                        VStack(alignment: .leading, spacing: 0) {
                            Text(section.title)
                                .font(.body.weight(.regular))

                            Divider()
                                .padding(.top, 10)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .id(section.id)
                        .listRowInsets(
                            EdgeInsets(top: 8, leading: 20, bottom: 0, trailing: 44)
                        )
                        .listRowSeparator(.hidden)
                        .listRowBackground(Color(uiColor: .systemBackground))

                        ForEach(section.contacts) { contact in
                            NavigationLink(value: contact) {
                                ContactRow(contact: contact)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .navigationLinkIndicatorVisibility(.hidden)
                            .listRowInsets(
                                EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 44)
                            )
                            .listRowBackground(Color(uiColor: .systemBackground))
                            .alignmentGuide(.listRowSeparatorLeading) { dimensions in
                                dimensions[.leading] + 56
                            }
                        }
                    }
                    Color.clear
                        .frame(height: 96)
                        .listRowInsets(EdgeInsets())
                        .listRowSeparator(.hidden)
                        .accessibilityHidden(true)
                }
                .overlay(alignment: .trailing) {
                    if contactModel.searchText.isEmpty {
                        ContactSectionIndex(
                            availableTitles: sections.map(\.title)
                        ) { title in
                            proxy.scrollTo(title, anchor: .top)
                        }
                    }
                }
                .listStyle(.plain)
                .scrollIndicators(.hidden)
                .scrollContentBackground(.hidden)
                .background(Color(uiColor: .systemBackground))
            }
        }
    }

    private var permissionRequiredContent: some View {
        ContentUnavailableView {
            Label("Allow Contacts Access", systemImage: "person.crop.circle.badge.checkmark")
        } description: {
            Text("Sim Deck reads your contacts to show names and let you call or message them. Contacts stay managed by iOS.")
        } actions: {
            Button("Continue") {
                Task {
                    await contactModel.requestAccess()
                }
            }
            .buttonStyle(.borderedProminent)
        }
    }

    private var permissionDeniedContent: some View {
        ContentUnavailableView {
            Label("Contacts Access Is Off", systemImage: "person.crop.circle.badge.exclamationmark")
        } description: {
            Text("Allow contact access in Settings to browse your iOS contacts in Sim Deck.")
        } actions: {
            Button("Open Settings") {
                guard let url = URL(string: UIApplication.openSettingsURLString) else { return }
                UIApplication.shared.open(url)
            }
            .buttonStyle(.borderedProminent)
        }
    }

    private var failedContent: some View {
        ContentUnavailableView {
            Label("Contacts Unavailable", systemImage: "exclamationmark.triangle")
        } description: {
            Text("Sim Deck could not read your contacts.")
        } actions: {
            Button("Try Again") {
                Task {
                    await contactModel.refresh()
                }
            }
            .buttonStyle(.bordered)
        }
    }
}

private struct ContactSectionIndex: View {
    let availableTitles: [String]
    let onSelect: (String) -> Void

    @State private var activeIndex: Int?

    var body: some View {
        GeometryReader { proxy in
            let trackHeight = ContactSectionIndexResolver.visibleTrackHeight
            let centeredTop = max((proxy.size.height - trackHeight) / 2, 0)

            VStack(spacing: 0) {
                Color.clear
                    .frame(height: max(centeredTop - 4, 0))
                    .accessibilityHidden(true)

                VStack(spacing: 0) {
                    ForEach(Array(ContactSectionIndexResolver.titles.enumerated()), id: \.offset) { index, title in
                        Text(title)
                            .font(.system(size: 11, weight: .regular))
                            .foregroundStyle(.tint)
                            .frame(height: ContactSectionIndexResolver.visibleItemHeight)
                            .opacity(activeIndex == nil || activeIndex == index ? 1 : 0.7)
                    }
                }
                .frame(width: 18, height: trackHeight)
                .frame(width: 44, alignment: .trailing)

                Spacer(minLength: 0)
            }
            .frame(width: proxy.size.width, height: proxy.size.height, alignment: .top)

            ContactSectionIndexTouchOverlay { locationY, isActive in
                guard isActive else {
                    activeIndex = nil
                    return
                }
                selectIndex(
                    for: locationY - centeredTop + 4,
                    height: trackHeight
                )
            }
            .frame(width: 44, height: proxy.size.height)
        }
        .frame(width: 44)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Contact index")
        .accessibilityHint("Drag vertically to jump between contact sections.")
    }

    private func selectIndex(for locationY: CGFloat, height: CGFloat) {
        let titles = ContactSectionIndexResolver.titles
        guard let index = ContactSectionIndexResolver.index(
            at: locationY,
            height: height,
            itemCount: titles.count
        ) else { return }
        guard index != activeIndex else { return }
        activeIndex = index
        guard let target = ContactSectionIndexResolver.targetTitle(
            for: titles[index],
            availableTitles: availableTitles
        ) else { return }
        UISelectionFeedbackGenerator().selectionChanged()
        onSelect(target)
    }
}

private struct ContactSectionIndexTouchOverlay: UIViewRepresentable {
    let onTouch: (CGFloat, Bool) -> Void

    func makeUIView(context: Context) -> ContactSectionIndexTouchView {
        let view = ContactSectionIndexTouchView()
        view.onTouch = onTouch
        return view
    }

    func updateUIView(_ view: ContactSectionIndexTouchView, context: Context) {
        view.onTouch = onTouch
    }
}

private final class ContactSectionIndexTouchView: UIView {
    var onTouch: ((CGFloat, Bool) -> Void)?

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .clear
        isOpaque = false
        isAccessibilityElement = false
    }

    required init?(coder: NSCoder) {
        super.init(coder: coder)
        backgroundColor = .clear
        isOpaque = false
        isAccessibilityElement = false
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        super.touchesBegan(touches, with: event)
        report(touches, isActive: true)
    }

    override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent?) {
        super.touchesMoved(touches, with: event)
        report(touches, isActive: true)
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent?) {
        super.touchesEnded(touches, with: event)
        report(touches, isActive: false)
    }

    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent?) {
        super.touchesCancelled(touches, with: event)
        report(touches, isActive: false)
    }

    private func report(_ touches: Set<UITouch>, isActive: Bool) {
        guard let touch = touches.first else { return }
        onTouch?(touch.location(in: self).y, isActive)
    }
}

private struct ContactRow: View {
    let contact: ContactRecord

    var body: some View {
        HStack(spacing: 12) {
            PersonAvatar(
                imageData: contact.thumbnailImageData,
                displayName: contact.displayName,
                organizationName: contact.organizationName,
                size: 46
            )

            VStack(alignment: .leading, spacing: 2) {
                Text(contact.displayName)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                    .lineLimit(1)

                if !contact.organizationName.isEmpty,
                   contact.organizationName != contact.displayName {
                    Text(contact.organizationName)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .frame(minHeight: 64)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(contact.displayName)
        .accessibilityValue(
            contact.phoneNumbers.isEmpty
                ? Text("No phone number")
                : Text("\(contact.phoneNumbers.count) phone numbers")
        )
    }
}

#if DEBUG
#Preview("Contacts - Loaded") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: PreviewContactFixtures.contacts)
        )
    }
}

#Preview("Contacts - Dark") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: PreviewContactFixtures.contacts)
        )
    }
    .preferredColorScheme(.dark)
}

#Preview("Contacts - Permission") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(authorizationState: .notDetermined),
            initialState: .permissionRequired,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Denied") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(authorizationState: .denied),
            initialState: .permissionDenied,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Loading") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(),
            initialState: .loading,
            startsSynchronization: false
        )
    }
}

#Preview("Contacts - Empty") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(contacts: [])
        )
    }
}

#Preview("Contacts - Error, Chinese, Accessibility") {
    NavigationStack {
        ContactsScreen(
            model: .preview(),
            contactStore: PreviewContactStore(),
            initialState: .failed,
            startsSynchronization: false
        )
    }
    .environment(\.locale, Locale(identifier: "zh-Hans"))
    .environment(\.dynamicTypeSize, .accessibility3)
}
#endif
