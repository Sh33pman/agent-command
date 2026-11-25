#!/usr/bin/env bash
# Packs acmd and agent-command as split .deb packages in <dist>:
#   acmd_<version>_<arch>.deb
#   agent-command_<version>_<arch>.deb
# Requires libghostty-vt (scripts/build-ghostty.sh) and GTK4/libadwaita/VTE4 dev
# packages. Must run on Linux (amd64 or arm64) with dpkg-deb available.
#   pack-deb.sh <version> <dist>
set -euo pipefail

[[ $# -eq 2 ]] || { sed -n '7p' "$0" >&2; exit 2; }
version=$1 dist=$2
root="$(cd "$(dirname "$0")/.." && pwd)"

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "pack-deb: unknown architecture $(uname -m)" >&2; exit 1 ;;
esac

if [[ ! -f "$root/build/ghostty/vt/include/ghostty/vt.h" ]]; then
	echo "pack-deb: libghostty-vt isn't built: run scripts/build-ghostty.sh first" >&2
	exit 1
fi
if ! pkg-config --exists gtk4 libadwaita-1 vte-2.91-gtk4; then
	echo "pack-deb: GTK4, libadwaita-1, and vte-2.91-gtk4 dev packages are required" >&2
	exit 1
fi

top="$(mktemp -d)"
trap 'rm -rf "$top"' EXIT

out="$top/out"
make -C "$root" build VERSION="$version" DIST="$out"
make -C "$root" linux-ui VERSION="$version" DIST="$out"

if ! file "$out/agent-command" | grep -q 'statically linked'; then
	echo "pack-deb: acmd isn't statically linked: $(file "$out/agent-command")" >&2
	exit 1
fi
got="$("$out/acmd" version)"
if [[ "$got" != "agent-command $version "* || "$got" != *"exact screen"* ]]; then
	echo "pack-deb: acmd says \"$got\": not version $version with the exact screen" >&2
	exit 1
fi

mkdir -p "$dist"
dist="$(cd "$dist" && pwd)"

# 1. acmd_<version>_<arch>.deb
acmd_pkg="acmd_${version}_${arch}.deb"
acmd_root="$top/acmd"
mkdir -p "$acmd_root/DEBIAN" "$acmd_root/usr/bin"
install -m 0755 "$out/agent-command" "$acmd_root/usr/bin/acmd"
install -m 0755 "$out/agent-command" "$acmd_root/usr/bin/agent-command"
cat >"$acmd_root/DEBIAN/control" <<EOF
Package: acmd
Version: $version
Section: utils
Priority: optional
Architecture: $arch
Maintainer: Agent Command <noreply@github.com>
Homepage: https://github.com/brettmostert/agent-command
Description: Agent Command CLI and session daemon
 Run AI coding agents and terminal sessions locally or across remote hosts.
EOF
dpkg-deb --root-owner-group --build "$acmd_root" "$dist/$acmd_pkg" >/dev/null
dpkg-deb --info "$dist/$acmd_pkg" >/dev/null
dpkg-deb --contents "$dist/$acmd_pkg" >/dev/null
echo "pack-deb: packed $dist/$acmd_pkg"

# 2. agent-command_<version>_<arch>.deb
ui_pkg="agent-command_${version}_${arch}.deb"
ui_root="$top/agent-command"
mkdir -p \
	"$ui_root/DEBIAN" \
	"$ui_root/usr/bin" \
	"$ui_root/usr/share/applications" \
	"$ui_root/usr/share/icons/hicolor/256x256/apps"
install -m 0755 "$out/agent-command-ui" "$ui_root/usr/bin/agent-command-ui"
install -m 0644 "$root/packaging/linux/com.brettmostert.agent-command.desktop" \
	"$ui_root/usr/share/applications/com.brettmostert.agent-command.desktop"
install -m 0644 "$root/packaging/linux/com.brettmostert.agent-command.png" \
	"$ui_root/usr/share/icons/hicolor/256x256/apps/com.brettmostert.agent-command.png"
cat >"$ui_root/DEBIAN/control" <<EOF
Package: agent-command
Version: $version
Section: x11
Priority: optional
Architecture: $arch
Depends: acmd (>= $version), libgtk-4-1, libadwaita-1-0, libvte-2.91-gtk4-0
Maintainer: Agent Command <noreply@github.com>
Homepage: https://github.com/brettmostert/agent-command
Description: Native Linux UI for Agent Command
 GTK4/libadwaita desktop application for managing Agent Command sessions.
EOF
for hook in postinst postrm; do
	cat >"$ui_root/DEBIAN/$hook" <<'EOF'
#!/bin/sh
set -e
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -f -t /usr/share/icons/hicolor >/dev/null 2>&1 || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
EOF
	chmod 0755 "$ui_root/DEBIAN/$hook"
done
dpkg-deb --root-owner-group --build "$ui_root" "$dist/$ui_pkg" >/dev/null
dpkg-deb --info "$dist/$ui_pkg" >/dev/null
dpkg-deb --contents "$dist/$ui_pkg" >/dev/null
echo "pack-deb: packed $dist/$ui_pkg"
