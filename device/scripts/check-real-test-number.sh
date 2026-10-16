#!/bin/sh

set -eu

number="${1:-}"
normalized="$(printf '%s' "$number" | tr -d ' ()-')"

case "$normalized" in
    13800000000|13500000000|+8613800000000|+8613500000000)
        printf '%s\n' "$normalized"
        ;;
    *)
        printf '%s\n' "Refusing real modem test destination: $number" >&2
        printf '%s\n' "Allowed destinations: 13800000000, 13500000000" >&2
        exit 64
        ;;
esac
