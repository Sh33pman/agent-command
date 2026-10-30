## What and why

<!-- One or two sentences. The PR title is the squash commit: use a Conventional Commit, e.g. "feat: add group picker". -->

## Checklist

- [ ] PR title is a Conventional Commit (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`, `ci:`; `!` or a `BREAKING CHANGE:` footer below for breaking changes)
- [ ] `make ci` passes locally
- [ ] All CI checks are green **before merging** (not enforced by GitHub on this plan; the release gate re-checks)
- [ ] Tests added/updated for the change
- [ ] SPEC.md updated if behaviour or design changed

<!-- Release PRs (chore(main): release x.y.z) only:
- [ ] `make it-ghostty` passed locally (can't run in CI)
-->
