#!/usr/bin/env bash
# Enforce coverage gates from a Go cover profile.
#   coverage-gate.sh <profile> <min-total-%> <min-core-%>
# The core gate applies to packages under internal/core once they exist.
# Writes a summary to $GITHUB_STEP_SUMMARY when running in GitHub Actions.
set -euo pipefail

profile=$1 min_total=$2 min_core=$3
module=$(go list -m)

pct() { # pct <profile> -> total statement coverage as a number
  go tool cover -func="$1" | awk '/^total:/ {sub("%", "", $3); print $3}'
}

total=$(pct "$profile")
status=0
summary="| Scope | Coverage | Minimum |\n|---|---|---|\n| total | ${total}% | ${min_total}% |"

if awk -v a="$total" -v b="$min_total" 'BEGIN {exit !(a < b)}'; then
  echo "FAIL: total coverage ${total}% < ${min_total}%"
  status=1
fi

core_lines=$(grep -E "^${module}/internal/core/" "$profile" || true)
if [[ -n "$core_lines" ]]; then
  core_profile=$(mktemp)
  trap 'rm -f "$core_profile"' EXIT
  { head -1 "$profile"; echo "$core_lines"; } >"$core_profile"
  core=$(pct "$core_profile")
  summary+="\n| internal/core | ${core}% | ${min_core}% |"
  if awk -v a="$core" -v b="$min_core" 'BEGIN {exit !(a < b)}'; then
    echo "FAIL: internal/core coverage ${core}% < ${min_core}%"
    status=1
  fi
fi

echo -e "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo -e "### Coverage\n\n$summary" >>"$GITHUB_STEP_SUMMARY"
fi
exit $status
