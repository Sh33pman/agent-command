#!/usr/bin/env bash
# Renders the Scoop manifest with dummy values and checks what Scoop reads:
# valid JSON, both architectures, the binary, and init and uninstall run from
# the app's folder.
set -euo pipefail

dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
a="$(printf 'a%.0s' {1..64})" b="$(printf 'b%.0s' {1..64})"
"$(dirname "$0")/render-scoop.sh" 9.8.7 "$a" "$b" "$dir/acmd.json" >/dev/null
m="$dir/acmd.json"
check() {
	local got
	got="$(jq -r "$1" "$m")"
	[[ "$got" == "$2" ]] || { echo "check-scoop: $1 is \"$got\", want \"$2\"" >&2; exit 1; }
}
check .version 9.8.7
check '.architecture."64bit".url' "https://github.com/brettmostert/homebrew-tap/releases/download/agent-command-v9.8.7/acmd-9.8.7-windows-amd64.zip"
check '.architecture."64bit".hash' "$a"
check '.architecture.arm64.url' "https://github.com/brettmostert/homebrew-tap/releases/download/agent-command-v9.8.7/acmd-9.8.7-windows-arm64.zip"
check '.architecture.arm64.hash' "$b"
check .bin acmd.exe
check .post_install '& "$dir\acmd.exe" init'
check .pre_uninstall '& "$dir\acmd.exe" uninstall --yes'
check .checkver.github https://github.com/brettmostert/agent-command
check '.autoupdate.hash.url' '$baseurl/checksums.txt'
echo "check-scoop: the manifest renders and reads as Scoop needs"
