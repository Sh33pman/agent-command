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
| `ci.yml` | Every PR | `make lint`, the speech engine, `make vuln`, `make secrets`, and `make test` → `make fuzz` → `make test-vt` → `make test-app` → build the app |
| `pr-title.yml` | PRs | Conventional Commit title check |
| CodeQL (default setup) | PRs, pushes to `main`, weekly | Scans the Go, Swift and workflow code, in the repo's Security tab. It isn't a required check |
| `ghostty-watch.yml` | Mondays | Opens an issue for a newer Ghostty release, or an advisory not in `.github/ghostty-advisories-reviewed.txt`. Dependabot can't watch Ghostty, as `scripts/build-ghostty.sh` pins it |
| `release.yml` | push to `main` | release-please; on a release: gate (`make ci fuzz`), then build and sign the app, publish it to the public tap and update the cask. |

Workflows run on GitHub-hosted runners: `macos-26` for anything that builds, `ubuntu-latest` for the rest. The repo is public, so they're free, and a PR from a fork never runs on anyone's own machine. `.github/actions/macos-tools` sets up Go, Zig and Xcode's Metal toolchain, and caches the Ghostty build. `main` needs a PR, and `lint`, `swift`, `vuln`, `macos` and `pr-title` must pass. Dependabot's titles start with `chore` or `ci`, so merging one doesn't make a release: it ships with the next `fix` or `feat`.

## Releases

1. Merge PRs with Conventional Commit titles.
2. release-please keeps a PR titled `chore(main): release X.Y.Z` up to date, with the version bump and `CHANGELOG.md`.
3. Merge it. The `release` workflow tags `vX.Y.Z`, runs the gate, builds and signs the app, publishes the public tap release `agent-command-vX.Y.Z`, and updates `Casks/agent-command.rb` in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap).
4. If the gate fails, nothing is published and the release is marked `[FAILED]`. Fix it and ship the next patch version. Never move or reuse tags.

Versioning is SemVer. Before 1.0, `feat` bumps the minor version and `fix` the patch. Breaking changes bump the minor version too.

## One-time repository setup

The GitHub App (`RELEASE_APP_CLIENT_ID` variable, `RELEASE_APP_PRIVATE_KEY` secret) opens release PRs and publishes to the tap.

Releases are signed with one long-lived self-signed certificate, because the project has no Apple Developer account. Create it once with `scripts/create-signing-cert.sh`, and store the results as the `ACMD_SIGNING_P12` and `ACMD_SIGNING_PASSWORD` secrets. `scripts/import-signing-cert.sh` imports it for a release; CI's `macos` job runs the same script with a throwaway certificate on every PR, so a release is never the first time signing runs on a fresh runner. `scripts/build-ghostty.sh` keeps its result in `~/.cache/agent-command/ghostty`, which CI caches. It rebuilds when the release, tools or script change, and deleting that folder forces a rebuild.

## Tool versions

Pinned in the `Makefile` (`make tools` installs them into `./bin`). GitHub Actions are pinned to commit SHAs and updated weekly by Dependabot.
