#!/usr/bin/env bash
# Packs acmd for Scoop as <dist>/acmd-<version>-windows-<arch>.zip, for amd64
# and arm64. Each holds acmd.exe and acmdw.exe, the logon task's launcher, at
# its top. Cross-compiled with cgo off, on any OS. It checks each binary names
# the version and is built for its architecture, and that acmdw opens no
# console window.
#   pack-windows.sh <version> <dist>
set -euo pipefail

[[ $# -eq 2 ]] || { sed -n '7p' "$0" >&2; exit 2; }
version=$1 dist=$2
root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$dist"
dist="$(cd "$dist" && pwd)"
top="$(mktemp -d)"
trap 'rm -rf "$top"' EXIT

for arch in amd64 arm64; do
	make -C "$root" build-windows WINDOWS_ARCH="$arch" VERSION="$version" DIST="$top"
	out="$top/windows-$arch"
	case "$arch" in
	amd64) machine="x86-64" ;;
	arm64) machine="(Aarch64|ARM64)" ;; # macOS's file says Aarch64, Linux's ARM64
	esac
	for exe in acmd.exe acmdw.exe; do
		info="$(go version -m "$out/$exe")"
		grep -q "GOOS=windows" <<<"$info" && grep -q "GOARCH=$arch" <<<"$info" || {
			echo "pack-windows: $exe isn't built for windows/$arch:" >&2
			echo "$info" >&2
			exit 1
		}
	done
	# -trimpath keeps -ldflags out of the build info, and a Windows binary
	# can't run here: the version is in it as a string.
	LC_ALL=C grep -aqF "$version" "$out/acmd.exe" || {
		echo "pack-windows: acmd.exe doesn't name version $version" >&2
		exit 1
	}
	# acmd.exe is a console program; acmdw.exe a GUI one, so no window opens.
	file "$out/acmd.exe" | grep -Eq "\(console\).*$machine" || { echo "pack-windows: acmd.exe: $(file "$out/acmd.exe")" >&2; exit 1; }
	file "$out/acmdw.exe" | grep -Eq "\(GUI\).*$machine" || { echo "pack-windows: acmdw.exe: $(file "$out/acmdw.exe")" >&2; exit 1; }
	name="acmd-$version-windows-$arch.zip"
	rm -f "$dist/$name"
	(cd "$out" && zip -q -X "$dist/$name" acmd.exe acmdw.exe)
	echo "pack-windows: packed $dist/$name"
done
