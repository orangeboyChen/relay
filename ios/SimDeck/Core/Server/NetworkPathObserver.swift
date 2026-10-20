import Foundation
import Network

@MainActor
final class NetworkPathObserver {
    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "com.nowcent.simdeck.network-path")
    private let onChange: @MainActor (Bool) -> Void
    private var hasReceivedPath = false
    private var isRunning = false

    init(onChange: @escaping @MainActor (Bool) -> Void) {
        self.onChange = onChange
    }

    func start() {
        guard !isRunning else { return }
        isRunning = true
        monitor.pathUpdateHandler = { [weak self] path in
            Task { @MainActor [weak self] in
                self?.handle(path)
            }
        }
        monitor.start(queue: queue)
    }

    private func handle(_ path: NWPath) {
        let isAvailable = path.status == .satisfied
        if !hasReceivedPath {
            hasReceivedPath = true
            // The first satisfied path is a baseline, not a connection change.
            guard !isAvailable else { return }
        }
        onChange(isAvailable)
    }
}
