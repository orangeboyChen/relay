// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "RelayProtocol",
    platforms: [.iOS(.v26), .macOS(.v26)],
    products: [
        .library(name: "RelayProtocol", targets: ["RelayProtocol"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.33.3"),
        .package(url: "https://github.com/connectrpc/connect-swift.git", from: "1.1.0"),
    ],
    targets: [
        .target(
            name: "RelayProtocol",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
                .product(name: "Connect", package: "connect-swift"),
            ]
        ),
    ]
)

