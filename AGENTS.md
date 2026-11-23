# Working on Agent Command

Instructions for coding agents working in this repo, whatever the harness. `CONTRIBUTING.md` explains PRs, CI and releases in full; this file covers how the maintainer wants work done.

## Before you write code

- **Read the ticket first**, and its parent if it has one. Work is tracked in Linear (team panda, project Agent Command). A parent ticket records the decisions already made, and the design is in the Linear document *Architecture: one daemon, many clients*. Don't reopen a recorded decision unless the code shows it's wrong.
- **Settle open questions with the maintainer, one at a time.** Give each question options, a recommendation and the reason for it. If nothing is open, say how you understand the requirement and the behaviour, and get a yes. After the last answer, summarise the whole plan and confirm it before building.
- **Be exact about platform limits.** Say precisely what's missing (for example "no ConPTY host yet", "no peer credentials on Windows") and whether it's a real limit or a choice. Don't present a choice as a limit.

## Building

- **Tests:** write tests of behaviour, not of how it's done. Keep coverage above 80%; `make test` enforces the coverage gates.
- **acmd is in beta.** Don't keep legacy or compatibility code; the rule of supporting old versions for 3 months starts at 1.0. Existing features must not break, Remote sessions especially.
- **One change updates every client.** A change to the wire or to an op updates the Go code, the macOS app (Swift, `app/`) and the Linux UI (Zig, `app-linux/`) in the same PR. The shared wire fixtures are in `internal/ipc/testdata/wire`. Regenerate them with `go test ./internal/ipc -update`, and the op catalog's golden files with `go test ./internal/catalog -update`.
- **Gates before a PR:** `make lint`, `make test`, `make test-app` and `make test-ui-core`. Run `make ci` where the machine can; it needs `scripts/build-ghostty.sh --prebuilt` once first.

## Shipping

Once the gates pass, ship without asking, with the `gh` CLI over HTTPS:

1. Make a branch, and a PR whose title is a Conventional Commit (`feat:`, `fix:`, `docs:` and so on). The title becomes the commit message.
2. Wait for every check to pass (`gh pr checks <n> --watch`), then squash-merge.
3. Merge release-please's release PR (`chore(main): release X.Y.Z`, labelled `autorelease: pending`).
4. Wait for the `release` workflow, and confirm the release published (`gh release list`).

If a release fails, fix it as the next patch version. Ask the maintainer before deleting a failed release draft. Never delete, move or reuse tags.

When a ticket is done, comment on it: what was built, what differs from the plan and why, and the PR and release. Then set it to Done. Close a parent ticket once all its sub-tickets are Done.

## Never

- Commit files you didn't create that are untracked in the clone, such as `.claude/`.
- Commit secrets, credentials or `.env` files.
- Put the maintainer's personal details (name, email) in anything sent to an outside service.

## Where things are

- **Install targets:** brew on macOS, Linux and WSL2 (WSL2 counts as Linux), and Scoop on Windows. Windows runs a client-only daemon: it connects to hosts, but doesn't run sessions.
- **The Go code:**
  - `cmd/agent-command` and `internal/cli`: the `acmd` command;
  - `internal/daemon`: the daemon;
  - `internal/ipc`: wire types and the local socket;
  - `internal/catalog`: the op catalog and its scopes;
  - `internal/remote`: Remote sessions, over mTLS and yamux.
- **The clients:** `app/` is the macOS app (Swift), and `app-linux/` the Linux UI (Zig).
- **Elsewhere:**
  - `helpers/acmd-speech`: the speech engine (Swift and MLX, macOS only);
  - `packaging/homebrew`: the cask;
  - `scripts/`: build and release.

## Writing

Use short sentences, and the same term for the same thing everywhere. That goes for code comments, docs, PR descriptions and tickets. In Linear, keep detail meant for agents in the *Agent notes* section at the end of a description.
