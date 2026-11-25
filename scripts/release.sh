#!/usr/bin/env bash
# The release workflow's steps that talk to GitHub (.github/workflows/release.yml).
# Each one is safe to run again, so "Re-run failed jobs" finishes a release.
#   release.sh notes <tag>                     print the draft release's notes
#   release.sh tap <version> <dist> <notes>    publish the app and acmd's archives to the tap, and
#                                              commit the cask and the formula
#   release.sh scoop <version> <dist>          commit the Scoop manifest, once the tap release is out
#   release.sh upload-linux <tag> <dist>       upload the .deb and Arch packages to the draft release
#   release.sh publish <tag>                   publish the draft release
#   release.sh failed <tag> <run-url>          mark the draft release as failed
# GH_TOKEN is the token for the repo the step changes: GITHUB_TOKEN for this
# repo, the release App's token for the tap and the bucket. TAP_REPO names the
# tap, and SCOOP_REPO the Scoop bucket.
set -euo pipefail
shopt -s inherit_errexit

repo="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"
tap="${TAP_REPO:-brettmostert/homebrew-tap}"
bucket="${SCOOP_REPO:-brettmostert/scoop-bucket}"
cask_path=Casks/agent-command.rb
formula_path=Formula/acmd.rb
scoop_path=bucket/acmd.json
# acmd's archives for the formula, by platform, as scripts/pack-acmd.sh names them.
platforms=(darwin-arm64 linux-amd64 linux-arm64)
# acmd's zips for Scoop, as scripts/pack-windows.sh names them.
windows=(windows-amd64 windows-arm64)
# Ends the note "failed" puts at the top of a draft's notes. notes and publish
# drop everything up to it.
mark="<!-- end of the release failure note -->"

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

asset_url() { echo "https://github.com/$tap/releases/download/agent-command-v$1/$2"; }

# The files a version's tap release holds: the app, acmd's archives and their checksums.
assets() {
	local p
	echo AgentCommand.zip
	for p in "${platforms[@]}"; do echo "acmd-$1-$p.tar.gz"; done
	for p in "${windows[@]}"; do echo "acmd-$1-$p.zip"; done
}

# Prints the SHA-256 of a file the tap serves for a version, as Homebrew downloads it.
published_sha() {
	local dir
	dir="$(mktemp -d)"
	curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -o "$dir/asset" "$(asset_url "$1" "$2")"
	sha256 "$dir/asset"
	rm -rf "$dir"
}

# Commits a rendered file to the tap's main through the API: GitHub signs the
# commit, and no token is written to disk. Passing the current file's blob SHA
# makes the commit fail, not overwrite, if the file changed since it was read.
commit_file() {
	local file=$1 path=$2 message=$3 to=${4:-$tap} blob err
	err="$(mktemp)"
	if ! blob="$(gh api "repos/$to/contents/$path?ref=main" --jq .sha 2>"$err")"; then
		grep -q 'HTTP 404' "$err" || { cat "$err" >&2; exit 1; }
		blob=""
	fi
	rm -f "$err"
	if [[ "$blob" == "$(git hash-object "$file")" ]]; then
		echo "release: $path is already up to date"
		return
	fi
	local args=(-X PUT "repos/$to/contents/$path" -f "message=$message"
		-f branch=main -f "content=$(base64 <"$file" | tr -d '\n')")
	[[ -z "$blob" ]] || args+=(-f "sha=$blob")
	gh api "${args[@]}" --silent
	echo "release: committed $path to $to"
}

# Prints a file on the tap's main once it names version, waiting a minute for
# main to catch up with a commit just made. Fails if it never does.
committed_at() {
	local path=$1 version=$2 committed try
	for try in 1 2 3 4 5 6; do
		committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$tap/contents/$path?ref=main")"
		if grep -qxF "  version \"$version\"" <<<"$committed"; then
			printf '%s\n' "$committed"
			return
		fi
		[[ $try == 6 ]] || sleep 10
	done
	echo "release: $path on main isn't at version $version" >&2
	exit 1
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
	local tag="agent-command-v$version" file releases id draft published="" got
	local files=()
	while read -r file; do
		[[ -f "$dist/$file" ]] || { echo "release: $dist/$file is missing" >&2; exit 1; }
		files+=("$dist/$file")
	done < <(assets "$version")

	# The tap release. Immutable releases are on in the tap, so its assets are
	# uploaded to a draft that gh then publishes. A draft left by a failed run is
	# replaced; a published release is kept, if it has this build's files.
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
		for file in "${files[@]}"; do
			got="$(published_sha "$version" "$(basename "$file")")"
			if [[ "$got" != "$(sha256 "$file")" ]]; then
				echo "release: the tap release $tag is already published with a different $(basename "$file")." >&2
				echo "release: it can't be changed, so ship the next patch version." >&2
				exit 1
			fi
		done
		echo "release: the tap release $tag is already published with this build"
	else
		gh release create "$tag" --repo "$tap" --title "agent-command $version" \
			--notes-file "$notes_file" "${files[@]}" "$dist/checksums.txt"
	fi

	# The cask, then the formula.
	local dir cask formula
	dir="$(mktemp -d)"
	cask="$dir/agent-command.rb"
	formula="$dir/acmd.rb"
	"$(dirname "$0")/render-cask.sh" "$version" "$(sha256 "$dist/AgentCommand.zip")" "$cask"
	"$(dirname "$0")/render-formula.sh" "$version" "$(sha256 "$dist/acmd-$version-darwin-arm64.tar.gz")" \
		"$(sha256 "$dist/acmd-$version-linux-amd64.tar.gz")" "$(sha256 "$dist/acmd-$version-linux-arm64.tar.gz")" "$formula"
	commit_file "$cask" "$cask_path" "agent-command $version"
	commit_file "$formula" "$formula_path" "acmd $version"
	rm -rf "$dir"

	# What a user gets: the cask and formula on main, and the files they download.
	local committed want url
	committed="$(committed_at "$cask_path" "$version")"
	want="$(sed -nE 's/^  sha256 "([0-9a-f]{64})"$/\1/p' <<<"$committed")"
	got="$(published_sha "$version" AgentCommand.zip)"
	if [[ "$got" != "$want" ]]; then
		echo "release: the published zip's SHA-256 is $got, but the cask has $want" >&2
		exit 1
	fi
	echo "release: the published zip matches the cask ($got)"
	committed="$(committed_at "$formula_path" "$version")"
	# Each url line is followed by its sha256 line.
	while read -r url want; do
		file="${url##*/}"
		got="$(published_sha "$version" "${file//'#{version}'/$version}")"
		if [[ "$got" != "$want" ]]; then
			echo "release: the published $file's SHA-256 is $got, but the formula has $want" >&2
			exit 1
		fi
		echo "release: the published $file matches the formula ($got)"
	done < <(awk '/^ *url "/ { gsub(/.*url "|".*/, ""); url = $0 }
		/^ *sha256 "/ { gsub(/.*sha256 "|".*/, ""); print url, $0 }' <<<"$committed")
	[[ "$(grep -c '^ *sha256 "' <<<"$committed")" == "${#platforms[@]}" ]] || {
		echo "release: the formula doesn't have one archive per platform" >&2
		exit 1
	}
}

# The Scoop manifest, last: it points at the tap release's zips, so it's
# committed only once they're out, and then checked against what Scoop
# downloads.
scoop() {
	local version=$1 dist=$2 dir manifest committed p file want got try
	[[ "$(gh api "repos/$tap/releases/tags/agent-command-v$version" --jq .draft)" == false ]] || {
		echo "release: the tap release agent-command-v$version isn't published: the manifest waits for it" >&2
		exit 1
	}
	dir="$(mktemp -d)"
	manifest="$dir/acmd.json"
	"$(dirname "$0")/render-scoop.sh" "$version" "$(sha256 "$dist/acmd-$version-windows-amd64.zip")" \
		"$(sha256 "$dist/acmd-$version-windows-arm64.zip")" "$manifest"
	commit_file "$manifest" "$scoop_path" "acmd $version" "$bucket"
	rm -rf "$dir"

	for try in 1 2 3 4 5 6; do
		committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$bucket/contents/$scoop_path?ref=main")"
		[[ "$(jq -r .version <<<"$committed")" == "$version" ]] && break
		[[ $try == 6 ]] && { echo "release: $scoop_path on main isn't at version $version" >&2; exit 1; }
		sleep 10
	done
	for p in 64bit arm64; do
		file="$(jq -r ".architecture.\"$p\".url" <<<"$committed")"
		want="$(jq -r ".architecture.\"$p\".hash" <<<"$committed")"
		got="$(published_sha "$version" "${file##*/}")"
		if [[ "$got" != "$want" ]]; then
			echo "release: the published ${file##*/}'s SHA-256 is $got, but the manifest has $want" >&2
			exit 1
		fi
		echo "release: the published ${file##*/} matches the manifest ($got)"
	done
}

upload_linux() {
	local tag=$1 dist=$2
	local version="${tag#v}" pkgver file
	pkgver="${version//-/_}"
	local files=()
	for file in \
		"acmd_${version}_amd64.deb" \
		"agent-command_${version}_amd64.deb" \
		"acmd_${version}_arm64.deb" \
		"agent-command_${version}_arm64.deb" \
		"acmd-${pkgver}-1-x86_64.pkg.tar.zst" \
		"agent-command-${pkgver}-1-x86_64.pkg.tar.zst"; do
		[[ -f "$dist/$file" ]] || { echo "release: $dist/$file is missing" >&2; exit 1; }
		files+=("$dist/$file")
	done
	gh release upload "$tag" "${files[@]}" --repo "$repo" --clobber
	echo "release: uploaded ${#files[@]} Linux packages to $tag"
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
	if committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$tap/contents/$formula_path?ref=main" 2>"$err")"; then
		if grep -qxF "  version \"$version\"" <<<"$committed"; then
			cask_line+=" The formula installs $version."
		else
			cask_line+=" The formula still installs an earlier version."
		fi
	elif grep -q 'HTTP 404' "$err"; then
		cask_line+=" There's no formula yet."
	else
		cask_line+=" The formula couldn't be checked."
	fi
	if committed="$(gh api -H 'Accept: application/vnd.github.raw' "repos/$bucket/contents/$scoop_path?ref=main" 2>"$err")"; then
		if [[ "$(jq -r .version <<<"$committed")" == "$version" ]]; then
			cask_line+=" The Scoop manifest installs $version."
		else
			cask_line+=" The Scoop manifest still installs an earlier version."
		fi
	elif grep -q 'HTTP 404' "$err"; then
		cask_line+=" There's no Scoop manifest yet."
	else
		cask_line+=" The Scoop manifest couldn't be checked."
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
	sed -n '2,11p' "$0" >&2
	exit 2
}
case "${1:-}" in
notes | publish) [[ $# -eq 2 ]] || usage ;;
failed | upload-linux) [[ $# -eq 3 ]] || usage ;;
tap) [[ $# -eq 4 ]] || usage ;;
scoop) [[ $# -eq 3 ]] || usage ;;
*) usage ;;
esac
if [[ "$1" == "upload-linux" ]]; then
	shift
	upload_linux "$@"
	exit 0
fi
"$@"
