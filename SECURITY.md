# Security

## Reporting a vulnerability

Please don't open a public issue. Contact the maintainer, GitHub user `brettmostert`, privately, with a description, steps to reproduce, and the output of `acmd version`.

## Scope

Agent Command runs locally, on macOS 26 or later. It adds hooks to your Claude Code settings (with a backup), listens on a unix socket only you can use, and runs Claude Code and shells in terminals it hosts. Of particular interest:

- Command injection through session names, folder paths or prompts
- Access to the sockets by other users
- Hooks influencing Claude Code's permission decisions (they must only observe)
- Sensitive data in the state database, logs or bug reports

## What's kept on disk

Everything below is readable only by you: folders are `0700`, and files with your data are `0600`.

In `~/.local/state/agent-command`:

- `env.json`: your login shell's environment, so sessions find their tools and credentials when the app starts them. It can include secrets your shell sets, such as API tokens.
- `state.db`: sessions, recent events and Inbox messages. Handled messages are deleted after 30 days. Open ones are kept until you handle them.
- `state.db.bak-*`: a copy of `state.db` from before each upgrade that changes it, kept for 30 days.
- `daemon.log`: one file per day, kept for 3 days.
- `backups/`: copies of your Claude Code settings from before each change.
- `speech/`: the speech model, if you install it. Its SHA-256 is checked when it's downloaded.

The sockets are in `$TMPDIR/agent-command-<uid>`, or `run/` in the folder above if that path would be too long for a socket.

## Known limits

- A session's first prompt is passed to `claude` on its command line, so other users on the same Mac can see it with `ps` while the session runs.
- Bug and feature reports are built on your Mac, and the log lines in them are redacted. The report opens as a new GitHub issue in your browser, and nothing is sent until you submit it.

## Verifying releases

Each release in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases) ships `checksums.txt` with the SHA-256 of `AgentCommand.zip`. The Homebrew cask pins the same SHA-256. Releases aren't signed or notarized with an Apple Developer ID.
