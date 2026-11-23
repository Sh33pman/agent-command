#!/usr/bin/env bash
# Renders the Homebrew formula, acmd.
#   render-formula.sh <version> <sha256 darwin-arm64> <sha256 linux-amd64> <sha256 linux-arm64> <output>
set -euo pipefail

[[ $# -eq 5 ]] || { sed -n '2,3p' "$0" >&2; exit 2; }
version=$1 darwin=$2 amd64=$3 arm64=$4 out=$5
tmpl="$(dirname "$0")/../packaging/homebrew/acmd.rb.tmpl"

for sha in "$darwin" "$amd64" "$arm64"; do
	[[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid sha256: $sha"; exit 1; }
done
mkdir -p "$(dirname "$out")"
sed -e "s|{{VERSION}}|${version}|" -e "s|{{SHA256_DARWIN_ARM64}}|${darwin}|" \
	-e "s|{{SHA256_LINUX_AMD64}}|${amd64}|" -e "s|{{SHA256_LINUX_ARM64}}|${arm64}|" "$tmpl" >"$out"
if grep -q '{{' "$out"; then
	echo "unrendered placeholders left in $out"
	exit 1
fi
echo "rendered $out"
