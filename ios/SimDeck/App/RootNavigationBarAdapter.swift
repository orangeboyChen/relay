// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import SwiftUI
import UIKit

@MainActor
final class RootNavigationBarAdapter: NSObject, UISearchResultsUpdating {
    private var searchText: Binding<String> = .constant("")
    private weak var navigationController: UINavigationController?
    private weak var configuredItem: UINavigationItem?
    private var title = ""
    private var usesLargeTitle = false
    private var showsSearch = false

    private lazy var searchController: UISearchController = {
        let controller = UISearchController(searchResultsController: nil)
        controller.obscuresBackgroundDuringPresentation = false
        controller.searchResultsUpdater = self
        controller.searchBar.autocapitalizationType = .none
        return controller
    }()

    func configure(
        navigationController: UINavigationController,
        title: String,
        usesLargeTitle: Bool,
        showsSearch: Bool,
        searchText: Binding<String>
    ) {
        self.navigationController = navigationController
        self.title = title
        self.usesLargeTitle = usesLargeTitle
        self.showsSearch = showsSearch
        self.searchText = searchText
        applyConfiguration()

        Task { @MainActor [weak self] in
            self?.applyConfiguration()
        }
    }

    private func applyConfiguration() {
        guard let navigationController,
              let viewController = navigationController.topViewController else {
            return
        }

        let item = viewController.navigationItem
        configuredItem = item
        viewController.title = title
        item.title = title
        navigationController.navigationBar.prefersLargeTitles = usesLargeTitle
        item.largeTitleDisplayMode = usesLargeTitle ? .always : .never

        if showsSearch {
            if searchController.searchBar.text != searchText.wrappedValue {
                searchController.searchBar.text = searchText.wrappedValue
            }
            item.searchController = searchController
            item.hidesSearchBarWhenScrolling = usesLargeTitle
            item.preferredSearchBarPlacement = .stacked
            if #available(iOS 26.0, *) {
                item.searchBarPlacementAllowsToolbarIntegration = false
            }
        } else if item.searchController === searchController {
            item.searchController = nil
        }

        navigationController.navigationBar.setNeedsLayout()
        navigationController.view.setNeedsLayout()
        navigationController.view.layoutIfNeeded()
    }

    func removeSearch() {
        guard configuredItem?.searchController === searchController else { return }
        configuredItem?.searchController = nil
    }

    func updateSearchResults(for searchController: UISearchController) {
        let value = searchController.searchBar.text ?? ""
        guard value != searchText.wrappedValue else { return }
        searchText.wrappedValue = value
    }
}
