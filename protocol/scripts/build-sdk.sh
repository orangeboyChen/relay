#!/usr/bin/env bash
set -euo pipefail

protocol_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$protocol_dir"
buf lint
buf generate
(cd sdk && GOWORK=off go test ./...)
swift build --package-path sdk

