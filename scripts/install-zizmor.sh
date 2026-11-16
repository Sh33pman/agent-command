#!/usr/bin/env bash
# Installs a pinned zizmor, the security linter for the workflow files, from
# its GitHub release. zizmor is written in Rust, so it can't be installed with
# go install like the other tools. The archive's SHA-256 is checked before it
# is unpacked.
#   install-zizmor.sh <dest-dir>
# To move to a new version: change VERSION and the four hashes, which the
# release page lists next to each archive.
set -euo pipefail

VERSION=v1.32.0
dest=$1

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) asset=zizmor-aarch64-apple-darwin.tar.gz sha=6efed18c4539a663fd3c851a2be9533fd8456c4e646d9416206da059154a0d3c ;;
Darwin-x86_64) asset=zizmor-x86_64-apple-darwin.tar.gz sha=6983143c719ada6845a69cc4dd2bacc215f971662fecd8bbd2678388e5ce5969 ;;
Linux-x86_64) asset=zizmor-x86_64-unknown-linux-gnu.tar.gz sha=bb110cc765eca9cd7efb2e284a956be5974c60027b0970f16397d86208a9957b ;;
Linux-aarch64) asset=zizmor-aarch64-unknown-linux-gnu.tar.gz sha=cb152b207f815116d7fa1c1ee4614f61578803351395052908095e79adfd9fd0 ;;
*)
	echo "install-zizmor: no zizmor build for $(uname -s) $(uname -m)" >&2
	exit 1
	;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -o "$tmp/$asset" \
	"https://github.com/zizmorcore/zizmor/releases/download/$VERSION/$asset"
if command -v shasum >/dev/null 2>&1; then
	got="$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)"
else
	got="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
fi
if [[ "$got" != "$sha" ]]; then
	echo "install-zizmor: $asset has SHA-256 $got, expected $sha" >&2
	exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp"
bin="$(find "$tmp" -type f -name zizmor | head -1)"
if [[ -z "$bin" ]]; then
	echo "install-zizmor: no zizmor binary in $asset" >&2
	exit 1
fi
mkdir -p "$dest"
install -m 0755 "$bin" "$dest/zizmor"
echo "install-zizmor: $VERSION in $dest"
