import SwiftUI

struct NewMessageScreen: View {
    let lines: [DeviceLineIdentity]

    @Environment(\.dismiss) private var dismiss
    @State private var selectedLineID: UUID?
    @State private var recipient = ""
    @State private var message = ""

    var body: some View {
        NavigationStack {
            Form {
                Picker("SIM", selection: $selectedLineID) {
                    Text("Choose a SIM").tag(UUID?.none)
                    ForEach(lines.filter(\.isOnline)) { line in
                        Text("SIM \(line.numericID) · \(line.displayName)").tag(Optional(line.id))
                    }
                }
                TextField("Recipient", text: $recipient)
                    .keyboardType(.phonePad)
                TextField("Message", text: $message, axis: .vertical)
                    .lineLimit(3...8)
            }
            .navigationTitle("New Message")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Send") { dismiss() }
                        .disabled(selectedLineID == nil || recipient.isEmpty || message.isEmpty)
                }
            }
        }
    }
}

#if DEBUG
#Preview("New message") {
    NewMessageScreen(lines: PreviewFixtures.devices)
}
#endif
