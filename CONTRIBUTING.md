# Contributing

## Workflow

- `main` is always releasable. Every change goes through a pull request and is **squash-merged**.
- The **PR title becomes the commit message** and must be a [Conventional Commit](https://www.conventionalcommits.org/): `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `test:`, `build:`, `ci:`, `chore:`, `revert:`. Mark breaking changes with `!` (`feat!: …`) or a `BREAKING CHANGE:` footer in the PR description. The `pr-title` check validates this.
- Run `make ci` before pushing. CI runs the same targets.
- **All checks must be green before merging.** GitHub can't enforce this on a private repo on the free plan, so it's a rule we follow. The release gate re-runs everything before publishing anything.

## CI (`.github/workflows`)

| Workflow | When | What |
|---|---|---|
| `ci.yml` | PRs (not docs-only) | ubuntu: `make lint`, `make vuln`. macOS: `make test` → `make fuzz` → `make snapshot` → `make formula-test` |
| `pr-title.yml` | PRs | Conventional Commit title check |
| `release.yml` | push to `main` | release-please; on a release: gate (`make ci fuzz snapshot formula-test` on the tag) → GoReleaser → binaries to the public tap → tested formula commit |
| `integration.yml` | manual (weekly from M2) | Kitty integration tests |

macOS minutes are billed at 10× against the free 2,000 min/month, so the macOS work is one job per workflow. Docs-only PRs skip CI.

## Releases

1. Merge PRs with Conventional Commit titles.
2. release-please keeps a PR titled `chore: release X.Y.Z` up to date, with the version bump and `CHANGELOG.md`.
3. Before merging it, run `make it-ghostty` locally (Ghostty integration can't run in CI).
4. Merge it. The `release` workflow tags `vX.Y.Z`, runs the gate, publishes the private GitHub Release assets and the public tap release `agent-command-vX.Y.Z`, and updates `Formula/agent-command.rb` in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap).
5. If the gate fails, nothing is published and the release is marked `[FAILED]`. Fix it and ship the next patch version. Never move or reuse tags.

Versioning is SemVer. Before 1.0, `feat` bumps the minor version and `fix` the patch. Breaking changes bump the minor version too.

## One-time repository setup

Recorded in SPEC.md §13.6: the GitHub App (`RELEASE_APP_CLIENT_ID` variable, `RELEASE_APP_PRIVATE_KEY` secret), merge settings, Actions permissions, Dependabot, retention and cost settings.

## Tool versions

Pinned in the `Makefile` (`make tools` installs them into `./bin`). GitHub Actions are pinned to commit SHAs and updated weekly by Dependabot.
