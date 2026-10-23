# Relay Protocol

Shared protobuf and Connect RPC contracts for encrypted communication synchronization.

The schema and generator configuration are the source of truth. Generated Go and Swift sources are not committed to this repository.

## Validate and build the SDK

```sh
./scripts/build-sdk.sh
```

The script generates an ignored `sdk/gen` and `sdk/Sources` tree, then compiles both packages. Tagged releases publish the same generated tree to `orangeboyChen/relay-sdk`, where Go Modules and Swift Package Manager can consume an exact version.
