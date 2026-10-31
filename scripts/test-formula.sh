#!/usr/bin/env bash
# Check a rendered formula from a throwaway local tap: `brew audit`, then
# either a full install + `brew test` + uninstall, or (safe mode) a fetch +
# checksum check and running the archived binary directly.
#
#   test-formula.sh <dist-dir>         render against the snapshot tarball (file:// URL)
#   test-formula.sh --formula <file>   test an already-rendered formula (release URL)
#
# Safe mode is used on self-hosted runners (RUNNER_ENVIRONMENT=self-hosted:
# the maintainer's own Mac and Homebrew) and whenever agent-command is
# already installed, so a real install is never touched.
set -euo pipefail

export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_ENV_HINTS=1

tap=acmd-ci/test
name=agent-command
work=$(mktemp -d)
installed=0
# Only uninstall what this script installed: in safe mode an unqualified
# uninstall could resolve to the user's real agent-command.
cleanup() {
  if [[ $installed -eq 1 ]]; then brew uninstall --formula "$tap/$name" >/dev/null 2>&1 || true; fi
  brew untap "$tap" >/dev/null 2>&1 || true
  brew untrust "$tap" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

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

safe=0
if [[ "${RUNNER_ENVIRONMENT:-}" == "self-hosted" || "${ACMD_FORMULA_SAFE:-}" == "1" ]] || brew list --formula "$name" >/dev/null 2>&1; then
  safe=1
fi

brew tap-new --no-git "$tap" >/dev/null
# Homebrew 6 Tap Trust: our own throwaway tap must be trusted to be loaded
# (and even untapped); cleanup untrusts it again.
brew trust "$tap" >/dev/null
cp "$formula" "$(brew --repository "$tap")/Formula/$name.rb"
brew audit "${audit_flags[@]}" --formula "$tap/$name"

if [[ $safe -eq 1 ]]; then
  echo "safe mode: not installing (self-hosted runner or agent-command already installed)"
  # Downloads the archive and verifies it against the formula's sha256.
  brew fetch --formula "$tap/$name" >/dev/null
  archive=$(brew --cache --formula "$tap/$name")
  [[ -f "$archive" ]] || { echo "brew fetch didn't produce an archive"; exit 1; }
  version=$(brew info --json=v2 "$tap/$name" | jq -r '.formulae[0].versions.stable')
  tar -xzf "$archive" -C "$work" agent-command
  out=$("$work/agent-command" version)
  echo "$out"
  [[ "$out" == "agent-command ${version} "* ]] || { echo "archive binary reports the wrong version (want $version)"; exit 1; }
  echo "formula OK (safe mode)"
  exit 0
fi

installed=1
brew install --formula "$tap/$name"
brew test "$tap/$name"
"$(brew --prefix)/bin/acmd" version
echo "formula OK"
