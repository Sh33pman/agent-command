#!/usr/bin/env bash
# Packs acmd for the brew formula as <dist>/acmd-<version>-<os>-<arch>.tar.gz.
# It holds one folder, acmd-<version>, which Homebrew unpacks into: bin/acmd and
# bin/agent-command (the same binary), and on macOS the speech engine in
# libexec/ (acmd-speech, with mlx.metallib next to it, where MLX looks).
#   pack-acmd.sh <version> <dist>         Linux: builds acmd with libghostty-vt
#                                         (run scripts/build-ghostty.sh first)
#   pack-acmd.sh <version> <dist> <from>  macOS: packs the signed acmd and speech
#                                         engine scripts/build-app.sh built in <from>
# Either way it checks that acmd names the version and keeps the exact screen.
set -euo pipefail

[[ $# -eq 2 || $# -eq 3 ]] || { sed -n '6,10p' "$0" >&2; exit 2; }
version=$1 dist=$2 from=${3:-}
root="$(cd "$(dirname "$0")/.." && pwd)"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "pack-acmd: unknown architecture $(uname -m)" >&2; exit 1 ;;
esac
name="acmd-$version-$os-$arch"

top="$(mktemp -d)"
trap 'rm -rf "$top"' EXIT
stage="$top/acmd-$version"
mkdir -p "$stage/bin"
case "$os" in
linux)
	[[ -z "$from" ]] || { echo "pack-acmd: on Linux, it builds acmd itself" >&2; exit 2; }
	if [[ ! -f "$root/build/ghostty/vt/include/ghostty/vt.h" ]]; then
		echo "pack-acmd: libghostty-vt isn't built: run scripts/build-ghostty.sh first" >&2
		exit 1
	fi
	make -C "$root" build VERSION="$version" DIST="$stage/out"
	cp "$stage/out/agent-command" "$stage/bin/acmd"
	# A static binary runs on any distribution.
	if ! file "$stage/bin/acmd" | grep -q 'statically linked'; then
		echo "pack-acmd: acmd isn't statically linked: $(file "$stage/bin/acmd")" >&2
		exit 1
	fi
	;;
darwin)
	[[ -n "$from" ]] || { echo "pack-acmd: on macOS, name the folder scripts/build-app.sh built in" >&2; exit 2; }
	mkdir -p "$stage/libexec"
	cp "$from/agent-command" "$stage/bin/acmd"
	cp "$from/acmd-speech" "$stage/libexec/acmd-speech"
	cp "$from/mlx.metallib" "$stage/libexec/mlx.metallib"
	# Copied, so still signed as build-app.sh signed them.
	codesign --verify --strict "$stage/bin/acmd" "$stage/libexec/acmd-speech"
	;;
*)
	echo "pack-acmd: no formula for $os" >&2
	exit 1
	;;
esac
cp "$stage/bin/acmd" "$stage/bin/agent-command"
rm -rf "$stage/out"

got="$("$stage/bin/acmd" version)"
if [[ "$got" != "agent-command $version "* || "$got" != *"exact screen"* ]]; then
	echo "pack-acmd: acmd says \"$got\": not version $version with the exact screen" >&2
	exit 1
fi
if [[ "$os" == darwin ]]; then
	"$stage/libexec/acmd-speech" --version
fi

mkdir -p "$dist"
# COPYFILE_DISABLE keeps macOS's ._ files out of the archive.
COPYFILE_DISABLE=1 tar -czf "$dist/$name.tar.gz" -C "$top" "acmd-$version"
echo "pack-acmd: packed $dist/$name.tar.gz ($got)"
