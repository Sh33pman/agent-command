#!/usr/bin/env bash
# The release workflow's steps that talk to GitHub (.github/workflows/release.yml).
# Each one is safe to run again, so "Re-run failed jobs" finishes a release.
#   release.sh notes <tag>                     print the draft release's notes
#   release.sh tap <version> <dist> <notes>    publish the app to the tap and commit the cask
#   release.sh publish <tag>                   publish the draft release
#   release.sh failed <tag> <run-url>          mark the draft release as failed
# GH_TOKEN is the token for the repo the step changes: GITHUB_TOKEN for this
# repo, the release App's token for the tap. TAP_REPO names the tap.
set -euo pipefail
shopt -s inherit_errexit

repo="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"
tap="${TAP_REPO:-brettmostert/homebrew-tap}"
cask_path=Casks/agent-command.rb
# Ends the note "failed" puts at the top of a draft's notes. notes and publish
# drop everything up to it.
mark="<!-- end of the release failure note -->"

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

zip_url() { echo "https://github.com/$tap/releases/download/agent-command-v$1/AgentCommand.zip"; }

# Prints the SHA-256 of the zip the tap serves for a version, as Homebrew downloads it.
published_sha() {
	local dir
	dir="$(mktemp -d)"
	curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -o "$dir/AgentCommand.zip" "$(zip_url "$1")"
	sha256 "$dir/AgentCommand.zip"
	rm -rf "$dir"
}

notes() {
	local body
	body="$(gh release view "$1" --repo "$repo" --json body --jq .body)"
	if [[ "$body" == *"$mark"* ]]; then
		body="${body#*"$mark"}"
		body="${body#$'\n'}"
		body="${body#$'\n'}"
	fi
	printf '%s\n' "$body"
}

tap() {
	local version=$1 dist=$2 notes_file=$3
	local tag="agent-command-v$version" zip="$dist/AgentCommand.zip"
	local sha releases id draft published="" got
	sha="$(sha256 "$zip")"

	# The tap release. Immutable releases are on in the tap, so its assets are
	# uploaded to a draft that gh then publishes. A draft left by a failed run is
	# replaced; a published release is kept, if it has this build's zip.
	releases="$(TAG="$tag" gh api --paginate "repos/$tap/releases?per_page=100" \
		--jq '.[] | select(.tag_name == env.TAG) | "\(.id) \(.draft)"')"
	while read -r id draft; do
		[[ -n "$id" ]] || continue
		if [[ "$draft" == true ]]; then
			echo "release: deleting the draft tap release $tag ($id) left by an earlier run"
			gh api -X DELETE "repos/$tap/releases/$id"
		else
			published=$id
		fi
	done <<<"$releases"
	if [[ -n "$published" ]]; then
		got="$(published_sha "$version")"
		if [[ "$got" != "$sha" ]]; then
			echo "release: the tap release $tag is already published with a different AgentCommand.zip." >&2
			echo "release: it can't be changed, so ship the next patch version." >&2
			exit 1
		fi
		echo "release: the tap release $tag is already published with this build"
	else
		gh release create "$tag" --repo "$tap" --title "agent-command $version" \
			--notes-file "$notes_file" "$zip" "$dist/checksums.txt"
	fi

	# The cask, committed through the API: GitHub signs the commit, and no token
	# is written to disk. Passing the current file's blob SHA makes the commit
	# fail, not overwrite, if the cask changed since it was read.
	local dir cask blob err
	dir="$(mktemp -d)"
	cask="$dir/agent-command.rb"
	err="$dir/err"
	"$(dirname "$0")/render-cask.sh" "$version" "$sha" "$cask"
	if ! blob="$(gh api "repos/$tap/contents/$cask_path?ref=main" --jq .sha 2>"$err")"; then
		grep -q 'HTTP 404' "$err" || { cat "$err" >&2; exit 1; }
		blob=""
	fi
	if [[ "$blob" == "$(git hash-object "$cask")" ]]; then
		echo "release: the cask is already at $version"
	else
		local args=(-X PUT "repos/$tap/contents/$cask_path" -f "message=agent-command $version"
			-f branch=main -f "content=$(base64 <"$cask" | tr -d '\n')")
		[[ -z "$blob" ]] || args+=(-f "sha=$blob")
		gh api "${args[@]}" --silent
		echo "release: committed the cask for $version"
	fi
	rm -rf "$dir"

	# What a user gets: the cask on main, and the zip it downloads.
	# Read right after the commit, main can still show the cask before it:
	# give it a minute to catch up before calling it wrong.
	local committed cask_version cask_sha try
	for try in 1 2 3 4 5 6; do
		committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$tap/contents/$cask_path?ref=main")"
		cask_version="$(sed -nE 's/^  version "([^"]+)"$/\1/p' <<<"$committed")"
		cask_sha="$(sed -nE 's/^  sha256 "([0-9a-f]{64})"$/\1/p' <<<"$committed")"
		[[ "$cask_version" == "$version" ]] && break
		[[ $try == 6 ]] || sleep 10
	done
	if [[ "$cask_version" != "$version" ]]; then
		echo "release: the cask on main is at version \"$cask_version\", not $version" >&2
		exit 1
	fi
	got="$(published_sha "$version")"
	if [[ "$got" != "$cask_sha" ]]; then
		echo "release: the published zip's SHA-256 is $got, but the cask has $cask_sha" >&2
		exit 1
	fi
	echo "release: the published zip matches the cask ($got)"
}

publish() {
	local tag=$1 body
	body="$(notes "$tag")"
	gh release edit "$tag" --repo "$repo" --title "$tag" --notes "$body" --draft=false
	echo "release: published $tag"
}

failed() {
	local tag=$1 run_url=$2
	local version="${tag#v}" draft tap_line cask_line committed err body
	draft="$(gh release view "$tag" --repo "$repo" --json isDraft --jq .isDraft)"
	if [[ "$draft" != true ]]; then
		echo "::warning::$tag is already published, so it's left as it is."
		exit 0
	fi

	# Says only what was checked: a run can fail after the tap release, or the
	# cask, was published.
	err="$(mktemp)"
	if gh api "repos/$tap/releases/tags/agent-command-v$version" --silent 2>"$err"; then
		tap_line="The tap release \`agent-command-v$version\` is published."
	elif grep -q 'HTTP 404' "$err"; then
		tap_line="Nothing was published to the tap."
	else
		tap_line="The tap release \`agent-command-v$version\` couldn't be checked."
	fi
	if committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$tap/contents/$cask_path?ref=main" 2>"$err")"; then
		if grep -qxF "  version \"$version\"" <<<"$committed"; then
			cask_line="The cask installs $version."
		else
			cask_line="The cask still installs an earlier version."
		fi
	else
		cask_line="The cask couldn't be checked."
	fi
	rm -f "$err"

	body="$(notes "$tag")"
	gh release edit "$tag" --repo "$repo" --title "[FAILED] $tag" --notes "$(
		printf '> **%s** See %s.\n>\n' "This release didn't finish, so it stays a draft." "$run_url"
		printf '> %s %s\n>\n' "$tap_line" "$cask_line"
		printf '> If the cause was outside the code, use "Re-run failed jobs" on that run. Otherwise delete this draft and ship the fix as the next patch version.\n'
		printf '%s\n\n%s' "$mark" "$body"
	)"
	echo "release: marked $tag as failed"
}

usage() {
	sed -n '2,9p' "$0" >&2
	exit 2
}
case "${1:-}" in
notes | publish) [[ $# -eq 2 ]] || usage ;;
failed) [[ $# -eq 3 ]] || usage ;;
tap) [[ $# -eq 4 ]] || usage ;;
*) usage ;;
esac
"$@"
