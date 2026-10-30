#!/usr/bin/env bash
# Install a rendered formula from a throwaway local tap, run `brew test` and
# `brew audit`, then uninstall and remove the tap again.
#
#   test-formula.sh <dist-dir>         render against the snapshot tarball (file:// URL)
#   test-formula.sh --formula <file>   test an already-rendered formula (release URL)
#
# Refuses to run if agent-command is already installed via Homebrew, so it
# never touches a real install.
set -euo pipefail

export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_ENV_HINTS=1

tap=acmd-ci/test
name=agent-command
work=$(mktemp -d)
trap 'brew uninstall --formula "$tap/$name" >/dev/null 2>&1 || true; brew untap "$tap" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

if [[ "${1:-}" == "--formula" ]]; then
  formula=$2
  audit_flags=(--strict --online)
else
  dist=$1
  version=$(jq -r '.version' "$dist/metadata.json")
  tarball="$dist/${name}_${version}_darwin_all.tar.gz"
  sha=$(shasum -a 256 "$tarball" | awk '{print $1}')
  formula="$work/$name.rb"
  # Whether Homebrew can infer the version from a snapshot archive name depends
  # on the commit hash (e.g. "...-SNAPSHOT-79a4d39_darwin_all" mis-parses,
  # "...-SNAPSHOT-a19b80e_darwin_all" doesn't). Pass an explicit version only
  # when needed; `brew audit --strict` rejects a redundant one.
  detected=$(HOMEBREW_ACMD_URL="file://$tarball" brew ruby -e 'puts Version.detect(ENV["HOMEBREW_ACMD_URL"])' | tail -1)
  explicit=""
  [[ "$detected" == "$version" ]] || explicit=$version
  echo "brew detects version '$detected' from the archive name; explicit version: '${explicit:-none}'"
  "$(dirname "$0")/render-formula.sh" "file://$tarball" "$sha" "$formula" $explicit
  # file:// URLs can't pass the online/https audits; those run on the release formula.
  audit_flags=(--strict)
fi

if brew list --formula "$name" >/dev/null 2>&1; then
  echo "agent-command is already installed via Homebrew; not touching it. Uninstall it first to run this test."
  exit 1
fi

brew tap-new --no-git "$tap" >/dev/null
cp "$formula" "$(brew --repository "$tap")/Formula/$name.rb"

brew install --formula "$tap/$name"
brew test "$tap/$name"
brew audit "${audit_flags[@]}" --formula "$tap/$name"
"$(brew --prefix)/bin/acmd" version
echo "formula OK"
