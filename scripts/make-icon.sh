#!/usr/bin/env bash
# Regenerates packaging/AppIcon.icns from scripts/make-icon.swift.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
swift "$root/scripts/make-icon.swift" "$work/icon.png"
set_dir="$work/AppIcon.iconset"
mkdir -p "$set_dir"
for s in 16 32 128 256 512; do
	sips -z "$s" "$s" "$work/icon.png" --out "$set_dir/icon_${s}x${s}.png" >/dev/null
	d=$((s * 2))
	sips -z "$d" "$d" "$work/icon.png" --out "$set_dir/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$set_dir" -o "$root/packaging/AppIcon.icns"
cp "$work/icon.png" "${1:-/dev/null}" 2>/dev/null || true
echo "make-icon: wrote packaging/AppIcon.icns"
