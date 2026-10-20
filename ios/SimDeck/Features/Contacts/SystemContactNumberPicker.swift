// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import ContactsUI
import SwiftUI

struct SystemContactNumberPicker: UIViewControllerRepresentable {
    let onSelect: (String) -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onSelect: onSelect)
    }

    func makeUIViewController(context: Context) -> CNContactPickerViewController {
        let controller = CNContactPickerViewController()
        controller.delegate = context.coordinator
        controller.displayedPropertyKeys = [CNContactPhoneNumbersKey]
        controller.predicateForSelectionOfProperty = NSPredicate(
            format: "key == %@",
            CNContactPhoneNumbersKey
        )
        return controller
    }

    func updateUIViewController(_ controller: CNContactPickerViewController, context: Context) {}

    final class Coordinator: NSObject, CNContactPickerDelegate {
        let onSelect: (String) -> Void

        init(onSelect: @escaping (String) -> Void) {
            self.onSelect = onSelect
        }

        func contactPicker(
            _ picker: CNContactPickerViewController,
            didSelect contactProperty: CNContactProperty
        ) {
            guard let phoneNumber = contactProperty.value as? CNPhoneNumber else { return }
            onSelect(phoneNumber.stringValue)
        }
    }
}
