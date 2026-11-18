#!/usr/bin/env bash
# Enforce coverage gates from a Go cover profile.
#   coverage-gate.sh <profile> <min-total-%> <min-core-%> [<min-remote-%>]
# The core gate applies to packages under internal/core, and the remote gate
# to packages under internal/remote, once they exist. The remote minimum
# defaults to the core one.
# Writes a summary to $GITHUB_STEP_SUMMARY when running in GitHub Actions.
set -euo pipefail

profile=$1 min_total=$2 min_core=$3 min_remote=${4:-$3}
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

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

gate() { # gate <dir under the module> <min-%>: checks the packages under dir
  local dir=$1 min=$2 lines part cov
  lines=$(grep -E "^${module}/${dir}/" "$profile" || true)
  [[ -n "$lines" ]] || return 0
  part="$scratch/${dir//\//_}.out"
  { head -1 "$profile"; echo "$lines"; } >"$part"
  cov=$(pct "$part")
  summary+="\n| ${dir} | ${cov}% | ${min}% |"
  if awk -v a="$cov" -v b="$min" 'BEGIN {exit !(a < b)}'; then
    echo "FAIL: ${dir} coverage ${cov}% < ${min}%"
    status=1
  fi
}

gate internal/core "$min_core"
gate internal/remote "$min_remote"

echo -e "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo -e "### Coverage\n\n$summary" >>"$GITHUB_STEP_SUMMARY"
fi
exit $status
