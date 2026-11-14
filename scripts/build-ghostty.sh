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

OS="$(uname -s)"
if command -v shasum >/dev/null 2>&1; then
	script_hash="$(shasum -a 256 "$0" | cut -c1-12)"
else
	script_hash="$(sha256sum "$0" | cut -c1-12)"
fi

# The result depends only on this script, the pinned release and the tools, so it
# is kept between builds. CI checkouts start clean, and this takes minutes.
cache_root="${ACMD_GHOSTTY_CACHE:-$HOME/.cache/agent-command/ghostty}"
if [[ "$OS" == "Darwin" ]]; then
	platform_key="sdk$(xcrun --show-sdk-version)"
else
	platform_key="$OS-$(uname -m)"
fi
key="$TAG-$COMMIT-zig$(zig version)-$platform_key-$script_hash"
cache="$cache_root/$key"

# Puts the built files where the build expects them.
install_from() {
	local from=$1
	mkdir -p "$out" "$root/app/vendor"
	rm -rf "$out/GhosttyKit.xcframework" "$out/vt"
	if [[ -d "$from/GhosttyKit.xcframework" ]]; then
		cp -R "$from/GhosttyKit.xcframework" "$out/GhosttyKit.xcframework"
		# SwiftPM needs a binary target inside the package.
		rm -rf "$root/app/vendor/GhosttyKit.xcframework"
		cp -R "$from/GhosttyKit.xcframework" "$root/app/vendor/GhosttyKit.xcframework"
	fi
	cp -R "$from/vt" "$out/vt"
	# The themes, which the macOS app needs without Ghostty.app installed.
	rm -rf "$root/app/vendor/ghostty-resources"
	if [[ -d "$from/ghostty-resources" ]]; then
		cp -R "$from/ghostty-resources" "$root/app/vendor/ghostty-resources"
	fi
}

if [[ -f "$cache/complete" ]]; then
	install_from "$cache"
	echo "build-ghostty: $TAG from the cache ($cache)"
	exit 0
fi

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
if [[ "$OS" == "Darwin" ]]; then
	zig build -Doptimize=ReleaseFast \
		-Demit-macos-app=false -Demit-xcframework=true -Dxcframework-target=native
fi
# For the CPU every runner has, not the one that built it: the cache is shared, and
# a library built for a runner with AVX-512 crashes (SIGILL) on one without.
cpu=()
[[ "$OS" == "Linux" ]] && cpu=(-Dcpu=baseline)
zig build -Doptimize=ReleaseFast -Demit-lib-vt=true ${cpu[@]+"${cpu[@]}"} --prefix "$out/vt"

# Built in a private folder and renamed into place, so runners working at the same
# time never see a partial cache. If another got there first, its copy stays.
mkdir -p "$cache_root"
partial="$(mktemp -d "$cache_root/partial.XXXXXX")"
if [[ "$OS" == "Darwin" ]]; then
	cp -R macos/GhosttyKit.xcframework "$partial/GhosttyKit.xcframework"
	mkdir -p "$partial/ghostty-resources"
	cp -R zig-out/share/ghostty/themes "$partial/ghostty-resources/themes"
fi
cp -R "$out/vt" "$partial/vt"
touch "$partial/complete"
if [[ -e "$cache" ]]; then
	rm -rf "$partial"
else
	mv "$partial" "$cache"
	# If another finished in that moment, mv put this copy inside it.
	rm -rf "$cache/$(basename "$partial")"
fi

install_from "$cache"

echo "build-ghostty: built $TAG in $out"
