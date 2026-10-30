#!/usr/bin/env bash
# Render the Homebrew formula from its template.
#   render-formula.sh <tarball-url> <sha256> <output.rb> [version]
# Pass [version] only when Homebrew can't infer it from the URL (snapshot
# archive names); for release URLs an explicit version fails `brew audit
# --strict` as redundant.
set -euo pipefail

url=$1 sha=$2 out=$3 version=${4:-}
tmpl="$(dirname "$0")/../packaging/homebrew/agent-command.rb.tmpl"

[[ "$sha" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid sha256: $sha"; exit 1; }
mkdir -p "$(dirname "$out")"
sed -e "s|{{URL}}|${url}|" -e "s|{{SHA256}}|${sha}|" "$tmpl" >"$out"
if [[ -n "$version" ]]; then
  # brew audit wants `version` before `sha256`.
  sed -i '' -e "s|^  sha256 \"${sha}\"|  version \"${version}\"\n&|" "$out"
  grep -q "^  version \"${version}\"" "$out" || { echo "failed to add version line"; exit 1; }
fi
if grep -q '{{' "$out"; then
  echo "unrendered placeholders left in $out"
  exit 1
fi
echo "rendered $out"
