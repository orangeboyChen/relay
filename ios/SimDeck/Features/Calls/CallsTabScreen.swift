import Foundation
import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var isDialerPresented = false

    var body: some View {
        CallHistoryScreen(
            state: model.callHistory,
            lines: model.devices,
            referenceDate: .now
        )
        .safeAreaInset(edge: .bottom) {
            DialerLauncherButton {
                isDialerPresented = true
            }
        }
        .sheet(isPresented: $isDialerPresented) {
            DialerScreen(
                lines: model.devices,
                initialLineID: preferredLineID,
                callStarter: model.callStarter
            ) { lineID in
                model.lastSuccessfulDialLineID = lineID
            }
            .presentationDetents([.large])
        }
    }

    private var preferredLineID: UUID? {
        if let lastID = model.lastSuccessfulDialLineID,
           model.devices.contains(where: { $0.id == lastID && $0.isOnline }) {
            return lastID
        }
        return model.devices.first(where: \.isOnline)?.id
    }
}

private struct DialerLauncherButton: View {
    let action: () -> Void

    var body: some View {
        HStack {
            Spacer()
            Button(action: action) {
                Image(systemName: "circle.grid.3x3.fill")
                    .font(.title2)
                    .frame(width: 56, height: 56)
                    .foregroundStyle(.white)
                    .background(.blue, in: Circle())
                    .shadow(radius: 5, y: 2)
            }
            .accessibilityLabel("Open dialer")
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
    }
}
