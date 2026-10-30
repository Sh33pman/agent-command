#!/usr/bin/env bash
# Run every Fuzz* target in the module for a short time each.
#   fuzz.sh [fuzztime]   (default 30s)
set -euo pipefail

fuzztime=${1:-30s}
found=0

for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    found=1
    echo "==> $pkg $target ($fuzztime)"
    go test "$pkg" -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime"
  done
done

if [[ $found -eq 0 ]]; then
  echo "no fuzz targets yet"
fi
