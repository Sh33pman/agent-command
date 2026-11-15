#!/usr/bin/env bash
# Checks the Homebrew cask template the way a release would use it: renders it
# with a dummy version and checksum, then runs brew style and brew audit on it.
# Without this, a mistake in the template is first found by a user running
# `brew upgrade`. Audit stays offline, so the dummy URL doesn't matter.
#   check-cask.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# brew audit wants a cask in a tap, so this makes a throwaway one.
tap="acmd-tmp/cask-check-$$"
tmp="$(mktemp -d)"
# Homebrew only loads casks from a tap it trusts. The trust is recorded in a
# private config folder, so this never changes the machine's own list.
export XDG_CONFIG_HOME="$tmp/config"
cleanup() {
	brew untap "$tap" >/dev/null 2>&1 || true
	# If untap couldn't, remove the tap by hand: nothing may stay behind.
	tap_dir="$(brew --repository "$tap")"
	rm -rf "$tap_dir"
	rmdir "$(dirname "$tap_dir")" 2>/dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT

"$root/scripts/render-cask.sh" 0.0.0 "$(printf '0%.0s' {1..64})" "$tmp/agent-command.rb"

brew tap-new --no-git "$tap" >/dev/null
# An older Homebrew has no trust list and no such command: it loads the tap as is.
brew trust "$tap" >/dev/null 2>&1 || true
casks="$(brew --repository "$tap")/Casks"
mkdir -p "$casks"
cp "$tmp/agent-command.rb" "$casks/"

# Both run even if the first fails, so one run shows everything to fix.
status=0
brew style --cask "$tap/agent-command" || status=1
brew audit --cask --strict "$tap/agent-command" || status=1
if [[ $status -eq 0 ]]; then
	echo "check-cask: the cask template passes brew style and brew audit"
fi
exit $status
