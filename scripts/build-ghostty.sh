#!/usr/bin/env bash
# Builds the two Ghostty libraries acmd v2 uses, from a pinned release:
#   GhosttyKit.xcframework  the app's terminal surfaces
#   libghostty-vt           the hosts' screen state
# Output goes to build/ghostty (git-ignored). Needs Xcode and the Zig version
# the release asks for.
set -euo pipefail

TAG=v1.4.0
COMMIT=83fb95624b

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/build/ghostty"
src="$out/src"

if [[ ! -d "$src/.git" ]]; then
	mkdir -p "$out"
	git clone --quiet --depth 1 --branch "$TAG" https://github.com/ghostty-org/ghostty.git "$src"
fi
actual="$(git -C "$src" rev-parse HEAD)"
if [[ "$actual" != "$COMMIT"* ]]; then
	echo "build-ghostty: $src is at $actual, expected $COMMIT ($TAG)" >&2
	exit 1
fi

cd "$src"
zig build -Doptimize=ReleaseFast \
	-Demit-macos-app=false -Demit-xcframework=true -Dxcframework-target=native
rm -rf "$out/GhosttyKit.xcframework"
cp -R macos/GhosttyKit.xcframework "$out/GhosttyKit.xcframework"

zig build -Doptimize=ReleaseFast -Demit-lib-vt=true --prefix "$out/vt"

# SwiftPM needs a binary target inside the package.
rm -rf "$root/app/vendor/GhosttyKit.xcframework"
mkdir -p "$root/app/vendor"
cp -R "$out/GhosttyKit.xcframework" "$root/app/vendor/GhosttyKit.xcframework"
# The themes, which the app needs without Ghostty.app installed.
rm -rf "$root/app/vendor/ghostty-resources"
mkdir -p "$root/app/vendor/ghostty-resources"
cp -R zig-out/share/ghostty/themes "$root/app/vendor/ghostty-resources/themes"

echo "build-ghostty: built $TAG in $out"
