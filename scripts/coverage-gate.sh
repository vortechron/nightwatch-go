#!/usr/bin/env sh
# Fails when total statement coverage is below the minimum (default 80).
# Usage: scripts/coverage-gate.sh [min] [profile]
set -eu
min="${1:-80}"
profile="${2:-coverage.out}"
total="$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%", "", $3); print $3}')"
echo "total coverage: ${total}% (minimum ${min}%)"
awk -v t="$total" -v m="$min" 'BEGIN { exit (t + 0 >= m + 0) ? 0 : 1 }' || {
  echo "coverage below ${min}%"
  exit 1
}
