#!/usr/bin/env bash
# Builds the two Ghostty libraries acmd v2 uses, from a pinned release:
#   GhosttyKit.xcframework  the app's terminal surfaces
#   libghostty-vt           the hosts' screen state
# Output goes to build/ghostty (git-ignored). Needs Xcode and the Zig version
# the release asks for.
#   build-ghostty.sh              build from the pinned source, or reuse an earlier build
#   build-ghostty.sh --prebuilt   macOS: install the prebuilt libraries pinned in
#                                 scripts/ghostty-prebuilt.pin, checked by SHA-256
#   build-ghostty.sh --pack FILE  macOS: pack what is in build/ghostty as that archive
# A release ships the prebuilt libraries, so nothing it ships comes from a cache.
# .github/workflows/ghostty-archive.yml builds and publishes them. After changing
# TAG or COMMIT, or how the libraries are built, run it and update the pin file.
set -euo pipefail

TAG=v1.4.0
COMMIT=83fb95624b0439ee660c2679b43b38c8b63769cd

root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/build/ghostty"
src="$out/src"

OS="$(uname -s)"
sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

# Puts the built files where the build expects them.
install_from() {
	local from=$1
	mkdir -p "$out" "$root/app/vendor"
	rm -rf "$out/GhosttyKit.xcframework" "$out/vt" "$out/lib" "$out/include" "$out/ghostty-resources"
	if [[ -d "$from/GhosttyKit.xcframework" ]]; then
		cp -R "$from/GhosttyKit.xcframework" "$out/GhosttyKit.xcframework"
		# SwiftPM needs a binary target inside the package.
		rm -rf "$root/app/vendor/GhosttyKit.xcframework"
		cp -R "$from/GhosttyKit.xcframework" "$root/app/vendor/GhosttyKit.xcframework"
	fi
	if [[ -d "$from/lib" ]]; then
		cp -R "$from/lib" "$out/lib"
	fi
	if [[ -d "$from/include" ]]; then
		cp -R "$from/include" "$out/include"
	fi
	cp -R "$from/vt" "$out/vt"
	# The themes, which the macOS/Linux apps need without Ghostty installed.
	rm -rf "$root/app/vendor/ghostty-resources"
	if [[ -d "$from/ghostty-resources" ]]; then
		cp -R "$from/ghostty-resources" "$out/ghostty-resources"
		if [[ "$OS" == "Darwin" ]]; then
			cp -R "$from/ghostty-resources" "$root/app/vendor/ghostty-resources"
		fi
	fi
}

# The prebuilt libraries: one archive, laid out like a build kept in the cache.
pin="$root/scripts/ghostty-prebuilt.pin"
parts=(GhosttyKit.xcframework vt ghostty-resources)
pin_field() { sed -nE "s/^$1=(.+)$/\1/p" "$pin" | head -1; }

prebuilt() {
	local commit url sha got
	if [[ "$OS" != "Darwin" ]]; then
		echo "build-ghostty: the prebuilt libraries are for macOS" >&2
		exit 1
	fi
	if [[ ! -f "$pin" ]]; then
		echo "build-ghostty: no prebuilt libraries are pinned ($pin is missing)" >&2
		exit 1
	fi
	commit="$(pin_field commit)"
	url="$(pin_field url)"
	sha="$(pin_field sha256)"
	# A changed pin above needs a new archive. This fails before anything is fetched.
	if [[ "$commit" != "$COMMIT" ]]; then
		echo "build-ghostty: $pin is for $commit, but this script pins $COMMIT ($TAG)." >&2
		echo "build-ghostty: run the ghostty-archive workflow and update the pin file." >&2
		exit 1
	fi
	if [[ -z "$url" || ! "$sha" =~ ^[0-9a-f]{64}$ ]]; then
		echo "build-ghostty: $pin needs a url and a sha256" >&2
		exit 1
	fi
	# Not local: the trap runs after this function has returned.
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -o "$tmp/ghostty.tar.gz" "$url"
	# Checked before it is unpacked: the hash is in the repo, reviewed in a PR, and
	# nothing that runs in CI can change it.
	got="$(sha256_of "$tmp/ghostty.tar.gz")"
	if [[ "$got" != "$sha" ]]; then
		echo "build-ghostty: the prebuilt archive's SHA-256 is $got, but $pin has $sha" >&2
		exit 1
	fi
	mkdir "$tmp/libs"
	tar -xzf "$tmp/ghostty.tar.gz" -C "$tmp/libs"
	if ! grep -qxF "commit=$COMMIT" "$tmp/libs/MANIFEST"; then
		echo "build-ghostty: the prebuilt archive wasn't built from $COMMIT" >&2
		exit 1
	fi
	install_from "$tmp/libs"
	echo "build-ghostty: $TAG from the prebuilt archive (sha256 $sha)"
}

pack() {
	local file=$1 part
	if [[ "$OS" != "Darwin" ]]; then
		echo "build-ghostty: the prebuilt libraries are for macOS" >&2
		exit 1
	fi
	# Not local: the trap runs after this function has returned.
	stage="$(mktemp -d)"
	trap 'rm -rf "$stage"' EXIT
	for part in "${parts[@]}"; do
		if [[ ! -d "$out/$part" ]]; then
			echo "build-ghostty: $out/$part is missing: build first" >&2
			exit 1
		fi
		cp -R "$out/$part" "$stage/$part"
	done
	# What the archive was built from and with. --prebuilt checks the commit.
	{
		echo "tag=$TAG"
		echo "commit=$COMMIT"
		echo "zig=$(zig version)"
		echo "sdk=$(xcrun --show-sdk-version)"
		echo "arch=$(uname -m)"
		if [[ -n "${GITHUB_RUN_ID:-}" ]]; then
			echo "run=$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID"
		fi
	} >"$stage/MANIFEST"
	mkdir -p "$(dirname "$file")"
	# COPYFILE_DISABLE keeps macOS's ._ files out of the archive.
	COPYFILE_DISABLE=1 tar -czf "$file" -C "$stage" "${parts[@]}" MANIFEST
	echo "build-ghostty: packed $TAG ($COMMIT) as $file"
}

case "${1:-}" in
"") ;;
--prebuilt)
	prebuilt
	exit 0
	;;
--pack)
	[[ $# -eq 2 ]] || { sed -n '2,13p' "$0" >&2; exit 2; }
	pack "$2"
	exit 0
	;;
*)
	sed -n '2,13p' "$0" >&2
	exit 2
	;;
esac

# The result depends only on this script, the pinned release and the tools, so it
# is kept between builds. CI checkouts start clean, and this takes minutes.
script_hash="$(sha256_of "$0" | cut -c1-12)"
cache_root="${ACMD_GHOSTTY_CACHE:-$HOME/.cache/agent-command/ghostty}"
if [[ "$OS" == "Darwin" ]]; then
	platform_key="sdk$(xcrun --show-sdk-version)"
else
	platform_key="$OS-$(uname -m)"
fi
key="$TAG-$COMMIT-zig$(zig version)-$platform_key-$script_hash"
cache="$cache_root/$key"

if [[ -f "$cache/complete" ]]; then
	install_from "$cache"
	echo "build-ghostty: $TAG from the cache ($cache)"
	exit 0
fi

if [[ ! -d "$src/.git" ]]; then
	mkdir -p "$out"
	# The commit is fetched by its full hash, not the tag: a tag can be moved, and
	# the hash can't be made to point at anything else.
	# A fetch that fails leaves nothing behind, so the next run starts again.
	rm -rf "$src"
	git init --quiet "$src"
	if ! git -C "$src" fetch --quiet --depth 1 https://github.com/ghostty-org/ghostty.git "$COMMIT" ||
		! git -C "$src" -c advice.detachedHead=false checkout --quiet FETCH_HEAD; then
		rm -rf "$src"
		echo "build-ghostty: couldn't fetch $COMMIT ($TAG)" >&2
		exit 1
	fi
fi
actual="$(git -C "$src" rev-parse HEAD)"
if [[ "$actual" != "$COMMIT" ]]; then
	echo "build-ghostty: $src is at $actual, expected $COMMIT ($TAG)" >&2
	exit 1
fi

cd "$src"
if [[ "$OS" == "Darwin" ]]; then
	zig build -Doptimize=ReleaseFast \
		-Demit-macos-app=false -Demit-xcframework=true -Dxcframework-target=native
else
	zig build -Doptimize=ReleaseFast -Dapp-runtime=none --prefix "$out/app"
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
else
	cp -R "$out/app/lib" "$partial/lib"
	cp -R "$out/app/include" "$partial/include"
	rm -rf "$out/app"
	# build.zig.zon records the themes archive's Zig package hash next to its URL.
	# It isn't a SHA-256 of the file, so only zig can check it: `zig fetch` unpacks
	# the archive and prints that hash. The same file is checked and extracted, so
	# nothing can change between the two.
	zon_field() {
		awk -v key="$1" '/\.iterm2_themes/ { in_pkg = 1 }
			in_pkg && $0 ~ "\\." key " *=" { gsub(/.*= *"|".*/, ""); print; exit }' build.zig.zon
	}
	themes_url="$(zon_field url)"
	themes_hash="$(zon_field hash)"
	if [[ -z "$themes_url" || -z "$themes_hash" ]]; then
		echo "build-ghostty: no themes url/hash in build.zig.zon" >&2
		exit 1
	fi
	themes_tmp="$(mktemp -d)"
	curl -fsSL "$themes_url" -o "$themes_tmp/themes.tgz"
	got="$(ZIG_GLOBAL_CACHE_DIR="$themes_tmp/zig-cache" zig fetch "$themes_tmp/themes.tgz")"
	if [[ "$got" != "$themes_hash" ]]; then
		echo "build-ghostty: themes archive hash is $got, build.zig.zon says $themes_hash" >&2
		rm -rf "$themes_tmp"
		exit 1
	fi
	mkdir -p "$partial/ghostty-resources/themes"
	tar -xzf "$themes_tmp/themes.tgz" --strip-components=1 -C "$partial/ghostty-resources/themes"
	rm -rf "$themes_tmp"
	rm -f "$partial/ghostty-resources/themes/"*.md
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
