#!/usr/bin/env bash

set -euo pipefail

coverage_profile="$(mktemp "${TMPDIR:-/tmp}/sim-deck-server-coverage.XXXXXX")"
trap 'rm -f "$coverage_profile"' EXIT

go test ./... -coverprofile="$coverage_profile"

excluded_pattern='^$'
coverage_label='repository'
if [[ -z "${TEST_DATABASE_URL:-}" ]]; then
  excluded_pattern='/internal/store/(postgres|device_discovery)\.go:'
  coverage_label='in-memory unit scope'
fi

awk -v excluded="$excluded_pattern" -v label="$coverage_label" '
  NR == 1 { next }
  $1 ~ excluded { next }
  {
    statements += $2
    if ($3 > 0) {
      covered += $2
    }
  }
  END {
    if (statements == 0) {
      print "coverage profile contains no statements" > "/dev/stderr"
      exit 1
    }
    percentage = covered * 100 / statements
    printf "%s coverage: %.1f%%\n", label, percentage
    if (percentage < 90) {
      printf "%s coverage is below 90%%\n", label > "/dev/stderr"
      exit 1
    }
  }
' "$coverage_profile"
