#!/usr/bin/env bash
# Renders the Scoop manifest, bucket/acmd.json in brettmostert/scoop-bucket.
#   render-scoop.sh <version> <sha256 windows-amd64> <sha256 windows-arm64> <output>
set -euo pipefail

[[ $# -eq 4 ]] || { sed -n '2,3p' "$0" >&2; exit 2; }
version=$1 amd64=$2 arm64=$3 out=$4
tmpl="$(dirname "$0")/../packaging/scoop/acmd.json.tmpl"

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid version: $version"; exit 1; }
for sha in "$amd64" "$arm64"; do
	[[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid sha256: $sha"; exit 1; }
done
mkdir -p "$(dirname "$out")"
sed -e "s|{{VERSION}}|${version}|g" -e "s|{{SHA256_WINDOWS_AMD64}}|${amd64}|" \
	-e "s|{{SHA256_WINDOWS_ARM64}}|${arm64}|" "$tmpl" >"$out"
if grep -q '{{' "$out"; then
	echo "unrendered placeholders left in $out"
	exit 1
fi
jq -e . "$out" >/dev/null || { echo "$out isn't valid JSON"; exit 1; }
echo "rendered $out"
