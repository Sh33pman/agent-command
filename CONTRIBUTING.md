# Contributing

## Workflow

- `main` is always releasable. Every change goes through a pull request and is **squash-merged**.
- The **PR title becomes the commit message** and must be a [Conventional Commit](https://www.conventionalcommits.org/): `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`, `revert:`. Mark breaking changes with `!` (`feat!: …`) or a `BREAKING CHANGE:` footer in the PR description. The `pr-title` check validates this.
- Run `make ci` before pushing. CI runs the same targets.
- Run `make hooks` once per clone: a `pre-push` git hook then runs `make lint` and `make test` (about 20 s) before every push, and stops the push if either fails. To push work in progress so it isn't lost (onto a branch or draft PR), `SKIP_CHECKS=1 git push` (or `git push --no-verify`) skips the checks once. Use it sparingly: CI still runs every check on the PR.
- **All checks must be green before merging.** GitHub can't enforce this on a private repo on the free plan, so it's a rule we follow. The release gate re-runs everything before publishing anything.

## CI (`.github/workflows`)

| Workflow | When | What |
|---|---|---|
| `ci.yml` | PRs (not docs-only) | ubuntu: `make lint`, `make vuln`, `make secrets`. macOS: `make test` → `make fuzz` → `make test-vt` → `make test-app` → build the app |
| `pr-title.yml` | PRs | Conventional Commit title check |
| `release.yml` | push to `main` | release-please; on a release: gate (`make ci fuzz`), then build and sign the app, publish it to the public tap and update the cask. |

All workflows run on **four self-hosted runners** on the maintainer's Mac (label `acmd`; SPEC §13.6), so CI isn't metered, but it waits while that Mac is asleep. Docs-only PRs skip CI. **Dependabot PRs need the `ok-to-test` label** before CI runs, because CI would execute the updated dependencies on that Mac. The Homebrew formula check never installs on a self-hosted runner (safe mode).

## Releases

1. Merge PRs with Conventional Commit titles.
2. release-please keeps a PR titled `chore(main): release X.Y.Z` up to date, with the version bump and `CHANGELOG.md`.
3. Before merging it, run `make it-ghostty` locally (Ghostty integration can't run in CI).
4. Merge it. The `release` workflow tags `vX.Y.Z`, runs the gate, builds and signs the app, publishes the public tap release `agent-command-vX.Y.Z`, and updates `Casks/agent-command.rb` in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap).
5. If the gate fails, nothing is published and the release is marked `[FAILED]`. Fix it and ship the next patch version. Never move or reuse tags.

Versioning is SemVer. Before 1.0, `feat` bumps the minor version and `fix` the patch. Breaking changes bump the minor version too.

## One-time repository setup

Recorded in SPEC.md §13.6: the GitHub App (`RELEASE_APP_CLIENT_ID` variable, `RELEASE_APP_PRIVATE_KEY` secret), merge settings, Actions permissions, Dependabot, retention and cost settings.

Releases are signed with one long-lived self-signed certificate, because the project has no Apple Developer account. Create it once with `scripts/create-signing-cert.sh`, and store the results as the `ACMD_SIGNING_P12` and `ACMD_SIGNING_PASSWORD` secrets. The runners need Xcode, including its Metal toolchain, and Zig. `scripts/build-ghostty.sh` keeps its result in `~/.cache/agent-command/ghostty`. It rebuilds when the release, tools or script change, and deleting that folder forces a rebuild.

## Tool versions

Pinned in the `Makefile` (`make tools` installs them into `./bin`). GitHub Actions are pinned to commit SHAs and updated weekly by Dependabot.
