#!/usr/bin/env bash
# Runs govulncheck and fails on a crash, a network failure, a format it does
# not understand, or any vulnerability not listed in .govulncheck-allow.
# Usage: scripts/vulncheck.sh
#
# govulncheck exits 0 when clean, 3 when it found vulnerabilities, and another
# number when it failed. Only 0 and a fully understood 3 can pass.
set -euo pipefail

VERSION=v1.9.0
root="$(cd "$(dirname "$0")/.." && pwd)"
allowfile="${VULN_ALLOWLIST:-$root/.govulncheck-allow}"
cd "$root"

tool="${GOVULNCHECK:-}"
if [ -z "$tool" ]; then
  # Build the pinned version with the Go toolchain this project asks for.
  toolchain="$(awk '$1 == "toolchain" {print $2}' go.mod)"
  bindir="$(go env GOPATH)/bin"
  if ! "$bindir/govulncheck" -version 2>/dev/null | grep -q "govulncheck@$VERSION"; then
    GOTOOLCHAIN="${toolchain:-auto}" GOBIN="$bindir" go install "golang.org/x/vuln/cmd/govulncheck@$VERSION"
  fi
  tool="$bindir/govulncheck"
fi

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

rc=0
"$tool" ./... >"$out" 2>&1 || rc=$?

ids="$(grep -E '^Vulnerability #[0-9]+: GO-[0-9]+-[0-9]+' "$out" | grep -oE 'GO-[0-9]+-[0-9]+' | sort -u || true)"
listed="$(grep -cE '^Vulnerability #[0-9]+: GO-' "$out" || true)"

case "$rc" in
  0)
    if [ -n "$ids" ]; then
      cat "$out"
      echo "vulncheck: exit 0 but vulnerabilities were printed, refusing to trust it" >&2
      exit 1
    fi
    echo "vulncheck: ok (no vulnerabilities)"
    exit 0
    ;;
  3) ;;
  *)
    cat "$out"
    echo "vulncheck: govulncheck failed with exit $rc (not a finding: a crash or a network error)" >&2
    exit 1
    ;;
esac

# Exit 3: the output must say how many it found, and we must have read that many.
summary="$(grep -oE 'Your code is affected by [0-9]+ vulnerabilit' "$out" | grep -oE '[0-9]+' || true)"
if [ -z "$ids" ] || [ -z "$summary" ] || [ "$summary" != "$listed" ]; then
  cat "$out"
  echo "vulncheck: found vulnerabilities but could not read the report (summary '$summary', listed '$listed'); the output format may have changed" >&2
  exit 1
fi

allowed="$(grep -v '^#' "$allowfile" 2>/dev/null | awk 'NF {print $1}' | sort -u || true)"
blocking=""
for id in $ids; do
  printf '%s\n' "$allowed" | grep -qx "$id" || blocking="$blocking $id"
done

if [ -n "$blocking" ]; then
  cat "$out"
  echo "vulncheck: not allowed:$blocking" >&2
  exit 1
fi

echo "vulncheck: ok (allowed: $(echo $ids))"
