# Contributing

## Workflow

- `main` is always releasable. Every change goes through a pull request and is **squash-merged**.
- The **PR title becomes the commit message** and must be a [Conventional Commit](https://www.conventionalcommits.org/): `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`, `revert:`. Mark breaking changes with `!` (`feat!: …`) or a `BREAKING CHANGE:` footer in the PR description. The `pr-title` check validates this.
- Run `make ci` before pushing. CI runs the same targets.
- Run `make hooks` once per clone: a `pre-push` git hook then runs `make lint` and `make test` (about 20 s) before every push, and stops the push if either fails. To push work in progress so it isn't lost (onto a branch or draft PR), `SKIP_CHECKS=1 git push` (or `git push --no-verify`) skips the checks once. Use it sparingly: CI still runs every check on the PR.
- **All checks must be green before merging.** Branch protection on `main` enforces it, and the release gate re-runs everything before publishing anything.

## CI (`.github/workflows`)

| Workflow | When | What |
|---|---|---|
| `ci.yml` | Every PR | `make lint`, the speech engine, `make vuln`, `make secrets`, and `make test` → `make fuzz` → `make test-vt` → `make test-app` → `make check-cask` (the Homebrew cask template passes `brew style` and `brew audit`) → build the app |
| `pr-title.yml` | PRs | Conventional Commit title check |
| `codeql.yml` | PRs, pushes to `main`, Mondays | CodeQL scans the Go (as macOS builds it), Swift and workflow code, into the repo's Security tab. Swift builds the app on the cached Ghostty build (for arm64: CodeQL's tracer runs under Rosetta), and the speech helper's own code; the helper with MLX, a long build of other people's code, only on `main` and Mondays, with findings in its dependencies (`.build/`) left out. It replaces GitHub's default setup, which must stay off for it to upload, and it isn't a required check |
| `ghostty-watch.yml` | Mondays | Opens an issue for a newer Ghostty release, or an advisory not in `.github/ghostty-advisories-reviewed.txt`. Dependabot can't watch Ghostty, as `scripts/build-ghostty.sh` pins it |
| `release.yml` | push to `main` | release-please; on a release, two jobs side by side: `gate` (`make ci`, `make fuzz`, `make test-vt test-app`, with no secrets) and `build` (build and sign the app from source, and attest its build provenance). Then `publish` releases the app to the public tap, commits the cask, checks the download against it, and publishes the release. `failed` marks the draft release if any of them fails |

Workflows run on GitHub-hosted runners: `macos-26` for anything that builds, `ubuntu-latest` for the rest. The repo is public, so they're free, and a PR from a fork never runs on anyone's own machine. `.github/actions/macos-tools` sets up Go, Zig and Xcode's Metal toolchain, and caches the Ghostty build and the speech helper (`SPEECH_CACHE`, keyed on its sources, the Makefile and Xcode's version, which saves about 10 minutes of building MLX). With its `cache` input set to `"false"`, as the release's `build` job does, Go, Zig and the speech helper are built from source. The Ghostty build still comes from the cache. CI skips the release PR and release commits, since the release gate runs every check on that commit. A PR counts as the release PR only if it is a branch of this repo named `release-please--…`, opened by the release App (`brettmostert-release[bot]`). The branch name alone doesn't skip anything, and renaming the App means updating that name in `ci.yml` and `codeql.yml`. `main` needs a PR, and `lint`, `swift`, `vuln`, `macos` and `pr-title` must pass. Dependabot's titles start with `chore` or `ci`, so merging one doesn't make a release: it ships with the next `fix` or `feat`.

## Releases

1. Merge PRs with Conventional Commit titles.
2. release-please keeps a PR titled `chore(main): release X.Y.Z` up to date, with the version bump and `CHANGELOG.md`.
3. Merge it. The `release` workflow tags `vX.Y.Z` and creates the GitHub Release as a draft. It runs the gate, and builds and signs the app. Then it publishes the public tap release `agent-command-vX.Y.Z`, updates `Casks/agent-command.rb` in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap), and checks that the zip Homebrew downloads matches the cask. Publishing the draft release is the last step, so a published release can always be installed.
4. If a job fails, the release stays a draft, titled `[FAILED] vX.Y.Z`. A note at the top of it links the run and says whether the tap release and the cask were published. If the cause was outside the code (a runner, the network, a setting), use **Re-run failed jobs** on that run: each job is safe to run again, and the build it publishes is the one already attested. Don't use **Re-run all jobs** once the tap release is published: it builds a different zip, which the tap release can't take. If the code needs a fix, delete the draft and ship the fix as the next patch version. The tag stays. Never move or reuse tags.

Versioning is SemVer. Before 1.0, `feat` bumps the minor version and `fix` the patch. Breaking changes bump the minor version too.

## One-time repository setup

The release App, `brettmostert-release` (`RELEASE_APP_CLIENT_ID` repository variable, `RELEASE_APP_PRIVATE_KEY` secret), opens release PRs, creates tags and releases, and publishes to the tap. Each job asks for a token with only what it needs: `contents` and `pull-requests` write on this repo for release-please, and `contents` write on the tap for publishing. Anything else on this repo uses the job's own `GITHUB_TOKEN`.

The three secrets, `ACMD_SIGNING_P12`, `ACMD_SIGNING_PASSWORD` and `RELEASE_APP_PRIVATE_KEY`, are in the `release` environment, which only `main` can deploy to. They aren't repository secrets. The `release-please`, `build` and `publish` jobs use that environment. `gate` doesn't, so the tests never run where the secrets are.

Tag rulesets block moving and deleting tags, and let only the release App create them: `v*` here, and `agent-command-v*` in the tap. The tap's `main` blocks deletion and force pushes. Immutable releases are on in the tap, so a published tap release's assets can't change; the workflow uploads them before it publishes.

Actions settings allow only GitHub's own actions, `mlugg/setup-zig`, `googleapis/release-please-action` and `amannn/action-semantic-pull-request`, and each must be pinned to a full commit SHA, with its version in a comment after it.

Releases are signed with one long-lived self-signed certificate, because the project has no Apple Developer account. Create it once with `scripts/create-signing-cert.sh`, and store the results as the `ACMD_SIGNING_P12` and `ACMD_SIGNING_PASSWORD` secrets in the `release` environment. `scripts/import-signing-cert.sh` imports it for a release; CI's `macos` job runs the same script with a throwaway certificate on every PR, so a release is never the first time signing runs on a fresh runner. `scripts/build-ghostty.sh` keeps its result in `~/.cache/agent-command/ghostty`, which CI caches. It rebuilds when the release, tools or script change, and deleting that folder forces a rebuild.

## Tool versions

Pinned in the `Makefile` (`make tools` installs them into `./bin`). GitHub Actions are pinned to commit SHAs and updated weekly by Dependabot.
