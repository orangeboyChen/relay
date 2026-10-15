#!/bin/sh
set -eu

profile="${TMPDIR:-/tmp}/sim-deck-device-coverage.$$.out"
trap 'rm -f "$profile"' EXIT

go test ./... -coverprofile="$profile"
coverage="$(go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"

awk -v coverage="$coverage" 'BEGIN {
  if (coverage + 0 < 90) {
    printf "repository statement coverage %s%% is below 90%%\n", coverage > "/dev/stderr"
    exit 1
  }
  printf "repository statement coverage: %s%%\n", coverage
}'
