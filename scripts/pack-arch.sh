#!/usr/bin/env bash
# Packs acmd and agent-command as split Arch Linux packages in <dist>:
#   acmd-<pkgver>-1-x86_64.pkg.tar.zst
#   agent-command-<pkgver>-1-x86_64.pkg.tar.zst
# Requires libghostty-vt (scripts/build-ghostty.sh) and gtk4/libadwaita/vte4.
# Must run on Arch Linux (x86_64) with makepkg available.
#   pack-arch.sh <version> <dist>
set -euo pipefail

[[ $# -eq 2 ]] || { sed -n '7p' "$0" >&2; exit 2; }
version=$1 dist=$2
pkgver="${version//-/_}"
root="$(cd "$(dirname "$0")/.." && pwd)"

if [[ "$(uname -m)" != "x86_64" ]]; then
	echo "pack-arch: expected x86_64, got $(uname -m)" >&2
	exit 1
fi
if [[ ! -f "$root/build/ghostty/vt/include/ghostty/vt.h" ]]; then
	echo "pack-arch: libghostty-vt isn't built: run scripts/build-ghostty.sh first" >&2
	exit 1
fi
if ! pkg-config --exists gtk4 libadwaita-1 vte-2.91-gtk4; then
	echo "pack-arch: gtk4, libadwaita, and vte4 are required" >&2
	exit 1
fi

top="$(mktemp -d)"
trap 'rm -rf "$top"' EXIT

work="$top/pkg"
mkdir -p "$work/src/out"
make -C "$root" build VERSION="$version" DIST="$work/src/out"
make -C "$root" linux-ui VERSION="$version" DIST="$work/src/out"

if ! file "$work/src/out/agent-command" | grep -q 'statically linked'; then
	echo "pack-arch: acmd isn't statically linked: $(file "$work/src/out/agent-command")" >&2
	exit 1
fi
got="$("$work/src/out/acmd" version)"
if [[ "$got" != "agent-command $version "* || "$got" != *"exact screen"* ]]; then
	echo "pack-arch: acmd says \"$got\": not version $version with the exact screen" >&2
	exit 1
fi

cp "$root/packaging/linux/com.brettmostert.agent-command.desktop" "$work/src/"
cp "$root/packaging/linux/com.brettmostert.agent-command.png" "$work/src/"
sed "s/{{PKGVER}}/$pkgver/g" "$root/packaging/arch/PKGBUILD.tmpl" >"$work/PKGBUILD"

if [[ "$(id -u)" -eq 0 ]]; then
	# makepkg refuses to run as root inside containers.
	chmod 755 "$top"
	chown -R nobody:nobody "$work"
	su -s /bin/bash nobody -c "cd '$work' && PKGDEST='$work' makepkg --nodeps -f"
else
	(cd "$work" && PKGDEST="$work" makepkg --nodeps -f)
fi

mkdir -p "$dist"
dist="$(cd "$dist" && pwd)"
for pkg in "acmd-$pkgver-1-x86_64.pkg.tar.zst" "agent-command-$pkgver-1-x86_64.pkg.tar.zst"; do
	[[ -f "$work/$pkg" ]] || { echo "pack-arch: missing expected package $pkg" >&2; exit 1; }
	cp "$work/$pkg" "$dist/$pkg"
	echo "pack-arch: packed $dist/$pkg"
done
