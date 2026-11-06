#!/usr/bin/env bash
# Renders the Homebrew cask.
#   render-cask.sh <version> <sha256> <output>
set -euo pipefail

version=$1 sha=$2 out=$3
tmpl="$(dirname "$0")/../packaging/homebrew/agent-command.rb.tmpl"

[[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid sha256: $sha"; exit 1; }
mkdir -p "$(dirname "$out")"
sed -e "s|{{VERSION}}|${version}|" -e "s|{{SHA256}}|${sha}|" "$tmpl" >"$out"
if grep -q '{{' "$out"; then
  echo "unrendered placeholders left in $out"
  exit 1
fi
echo "rendered $out"
