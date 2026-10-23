# Sim Deck

Sim Deck connects one or more cellular modems to an iPhone for encrypted messaging and calling.

## Repository layout

- `protocol/` contains the protobuf definitions and generated Go and Swift clients.
- `server/` contains the Go control plane and PostgreSQL persistence layer.
- `device/` contains the Go modem agent and container tooling.
- `ios/` contains the SwiftUI application and its extensions.

The components remain independently buildable and deployable. The monorepo keeps protocol and implementation changes atomic while the release workflow publishes separate server and device container images.

## Development

Run Go tests from the repository root with:

```sh
go test ./server/... ./device/... ./protocol/...
```

Build the protocol Swift package with:

```sh
swift build --package-path protocol
```

The iOS project is located at `ios/SimDeck.xcodeproj`.

## Releases

The manual `Release` workflow creates both multi-architecture images and one GitHub release:

- `ghcr.io/orangeboychen/sim-deck-server:X.Y.Z`
- `ghcr.io/orangeboychen/sim-deck-device:X.Y.Z`
- Git tag `vX.Y.Z`

Legacy tags are retained with `server/`, `device/`, and `protocol/` prefixes.

## Licensing

Each component retains the license and legal notices in its own directory.

