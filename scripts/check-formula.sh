#!/usr/bin/env bash
# Checks the Homebrew formula template, acmd, the way a release would use it:
# renders it with a dummy version and checksums, then runs brew style and brew
# audit on it. Audit stays offline, so the dummy URLs don't matter.
#   check-formula.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# brew audit wants a formula in a tap, so this makes a throwaway one.
tap="acmd-tmp/formula-check-$$"
tmp="$(mktemp -d)"
# Homebrew only loads formulae from a tap it trusts. The trust is recorded in a
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

zero="$(printf '0%.0s' {1..64})"
"$root/scripts/render-formula.sh" 0.0.0 "$zero" "$zero" "$zero" "$tmp/acmd.rb"

brew tap-new --no-git "$tap" >/dev/null
# An older Homebrew has no trust list and no such command: it loads the tap as is.
brew trust "$tap" >/dev/null 2>&1 || true
formulae="$(brew --repository "$tap")/Formula"
mkdir -p "$formulae"
cp "$tmp/acmd.rb" "$formulae/"

# Both run even if the first fails, so one run shows everything to fix.
status=0
brew style --formula "$tap/acmd" || status=1
brew audit --formula --strict "$tap/acmd" || status=1
if [[ $status -eq 0 ]]; then
	echo "check-formula: the formula template passes brew style and brew audit"
fi
exit $status
