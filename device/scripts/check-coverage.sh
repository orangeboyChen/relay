#!/bin/sh
set -eu

profile="${TMPDIR:-/tmp}/sim-deck-device-coverage.$$.out"
trap 'rm -f "$profile"' EXIT

go test ./... -coverprofile="$profile"

# These files are process and hardware integration boundaries. They are
# compiled and exercised by container/device integration tests, but cannot be
# meaningfully driven by the in-memory unit suite without a modem or ICE stack.
excluded_pattern='^(sim-deck-device/cmd/sim-deck-device/main.go:|sim-deck-device/internal/media/(peer|remote_pcm)\.go:|sim-deck-device/internal/modem/session\.go:)'
coverage="$(awk -v excluded="$excluded_pattern" '
  NR == 1 { next }
  $1 ~ excluded { next }
  { statements += $2; if ($3 > 0) covered += $2 }
  END { printf "%.1f", covered * 100 / statements }
' "$profile")"

awk -v coverage="$coverage" 'BEGIN {
  if (coverage + 0 < 90) {
    printf "unit statement coverage %s%% is below 90%%\n", coverage > "/dev/stderr"
    exit 1
  }
  printf "unit statement coverage: %s%%\n", coverage
}'
