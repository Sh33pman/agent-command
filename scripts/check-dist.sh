#!/usr/bin/env bash
# Sanity-check GoReleaser output: expected archives exist, checksums verify,
# and the native binary reports the version GoReleaser built.
#   check-dist.sh <dist-dir>
set -euo pipefail

dist=$1
version=$(jq -r '.version' "$dist/metadata.json")
project=$(jq -r '.project_name' "$dist/metadata.json")

for arch in arm64 amd64 all; do
  f="$dist/${project}_${version}_darwin_${arch}.tar.gz"
  [[ -f "$f" ]] || { echo "missing archive: $f"; exit 1; }
done

(cd "$dist" && shasum -a 256 -c checksums.txt)

ls "$dist"/*.sbom.json >/dev/null

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$dist/${project}_${version}_darwin_all.tar.gz" -C "$tmp"
out=$("$tmp/agent-command" version)
echo "$out"
[[ "$out" == "agent-command ${version} "* ]] || { echo "unexpected version output (want ${version})"; exit 1; }
lipo -archs "$tmp/agent-command" | grep -q arm64
lipo -archs "$tmp/agent-command" | grep -q x86_64
# the Swift speech engine: present, runs, arm64, no GPL espeak linked
"$tmp/acmd-speech" --version
lipo -archs "$tmp/acmd-speech" | grep -q arm64
if otool -L "$tmp/acmd-speech" | grep -qi espeak; then echo "acmd-speech links espeak"; exit 1; fi
echo "dist OK: ${project} ${version}"
