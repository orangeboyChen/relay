import Foundation
import SwiftUI

struct CallsTabScreen: View {
    @Bindable var model: AppModel

    @State private var dialerPresentation: DialerPresentation?

    var body: some View {
        CallHistoryScreen(
            state: model.callHistory,
            lines: model.devices,
            referenceDate: .now,
            callStarter: model.callCoordinator
        ) { lineID in
            model.lastSuccessfulDialLineID = lineID
        }
        .safeAreaInset(edge: .bottom) {
            DialerLauncherButton {
                dialerPresentation = .manual()
            }
        }
        .sheet(item: $dialerPresentation) { presentation in
            DialerScreen(
                lines: model.devices,
                initialLineID: initialLineID(for: presentation),
                initialNumber: presentation.number,
                callStarter: model.callCoordinator
            ) { lineID in
                model.lastSuccessfulDialLineID = lineID
            }
            .presentationDetents([.large])
        }
        .onChange(of: model.pendingExternalDialRequest, initial: true) { _, request in
            presentExternalDialRequestIfPossible(request)
        }
        .onChange(of: model.callCoordinator.activeCall?.id) { _, callID in
            guard callID == nil else { return }
            presentExternalDialRequestIfPossible(model.pendingExternalDialRequest)
        }
    }

    private var preferredLineID: UUID? {
        if let defaultID = model.defaultCallLineID,
           model.devices.contains(where: { $0.id == defaultID && $0.isOnline }) {
            return defaultID
        }
        if let lastID = model.lastSuccessfulDialLineID,
           model.devices.contains(where: { $0.id == lastID && $0.isOnline }) {
            return lastID
        }
        return model.devices.first(where: \.isOnline)?.id
    }

    private var externalPreferredLineID: UUID? {
        guard case .ready(let line) = OutgoingCallLineRouter.resolve(
            lines: model.devices,
            preferredLineID: model.defaultCallLineID
        ) else {
            return nil
        }
        return line.id
    }

    private func presentExternalDialRequestIfPossible(_ request: ExternalDialRequest?) {
        guard let request, model.callCoordinator.activeCall == nil else { return }
        dialerPresentation = .external(request)
        model.pendingExternalDialRequest = nil
    }

    private func initialLineID(for presentation: DialerPresentation) -> UUID? {
        guard let lineSelection = presentation.lineSelection else { return preferredLineID }
        switch lineSelection {
        case .automatic:
            return externalPreferredLineID
        case .preferred(let lineID):
            return model.devices.contains(where: { $0.id == lineID && $0.isOnline })
                ? lineID
                : nil
        }
    }
}

private struct DialerPresentation: Identifiable {
    let id: UUID
    let number: String
    let lineSelection: ExternalDialLineSelection?

    static func manual() -> DialerPresentation {
        DialerPresentation(id: UUID(), number: "", lineSelection: nil)
    }

    static func external(_ request: ExternalDialRequest) -> DialerPresentation {
        DialerPresentation(
            id: request.id,
            number: request.number,
            lineSelection: request.lineSelection
        )
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
