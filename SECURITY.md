# Security

## Reporting a vulnerability

Please don't open a public issue. Report it privately with GitHub's [Report a vulnerability](https://github.com/brettmostert/agent-command/security/advisories/new) button, with a description, steps to reproduce, and the output of `acmd version`.

For a problem with Remote sessions, say so in the title, and include the output of `acmd remote status` and `acmd remote list` on each Mac involved, and the `remote:` lines from the daemon log (`~/.local/state/agent-command/daemon.log`). Fingerprints are fine to share. Never send a `key.pem` or any other private key.

## Scope

Agent Command runs locally, on macOS 26 or later. It adds hooks to your Claude Code settings (with a backup), listens on a unix socket only you can use, and runs Claude Code and shells in terminals it hosts. With Remote sessions it can also listen on the network (see [Network surface](#network-surface-remote-sessions)). Of particular interest:

- Command injection through session names, folder paths or prompts
- Access to the sockets by other users
- Hooks influencing Claude Code's permission decisions (they must only observe)
- Sensitive data in the state database, logs or bug reports
- Getting past Hosting's checks: connecting without an authorized fingerprint, reaching a session that isn't Remote, or a host or client being impersonated

## Network surface: Remote sessions

Agent Command listens on the network only when **Hosting** is on, for [Remote sessions](docs/remote-sessions.md). It's off by default.

- **What listens:** the daemon, on one TCP port: 7433 unless you choose another, or a free port it picks when 7433 is taken and `port_fallback` is on (the default). It listens on `127.0.0.1` unless you set Listen on to All interfaces (`remote.host.listen = "all"`). Turning Hosting off closes the port and every connection.
- **Authentication:** TLS 1.3 only, and mutual: both sides show a self-signed cert, and each checks the other's by its SHA-256 fingerprint, pinned ahead of time. There's no certificate authority, so a fingerprint is the only thing trusted. The host lets in only clients in `[[remote.authorized_clients]]`. An unknown client is refused, and a request is kept for you to approve or deny; nothing is approved by itself. Requests are rate limited (10 new ones a minute, 50 at most), and expire 24 hours after the client last tried. A client checks the host's fingerprint in the TLS handshake, before any call, and doesn't reconnect after a mismatch until you change the pinned one.
- **What an authorized client can do:** see and fully control the host's Remote sessions (type in them, read their screens and Inbox messages, rename and end them), and start new sessions on the host, including shells, in any folder your account can reach. It never sees the host's other sessions. Authorizing a client amounts to giving it your account on the host, so authorize only machines you control. Revoking it (`acmd remote revoke`) drops it at once.
- **Keys:** acmd's own identity is an Ed25519 key in `~/.config/agent-command/identity/key.pem` (`0600`, in a `0700` folder). Your own cert and key can be used instead; acmd refuses a key that others can read, or one with a passphrase.
- **As a client,** acmd only connects out, to the hosts in `[[remote.hosts]]`, and opens no port.
- **Logs:** each connection, disconnection, request and refused handshake is logged in `daemon.log`, with the peer's fingerprint and address.

Agent Remote Control is a different thing: it starts Claude and Antigravity with `--remote-control`, and their own services carry that traffic. acmd doesn't listen for it.

## What's kept on disk

Everything below is readable only by you: folders are `0700`, and files with your data are `0600`.

In `~/.local/state/agent-command`:

- `env.json`: your login shell's environment, so sessions find their tools and credentials when the app starts them. It can include secrets your shell sets, such as API tokens. Values that change with each login, like the SSH agent's socket, aren't saved.
- `state.db`: sessions, recent events and Inbox messages. Handled messages are deleted after 30 days. Open ones are kept until you handle them.
- `state.db.bak-*`: a copy of `state.db` from before each upgrade that changes it, kept for 30 days.
- `daemon.log`: one file per day, kept for 3 days.
- `backups/`: copies of your Claude Code settings from before each change.
- `speech/`: the speech model, if you install it. Its SHA-256 is checked when it's downloaded.
- `remote-host.json`: Hosting's pending requests (each client's fingerprint, cert name and address), and the port the fallback picked.

In `~/.config/agent-command`, `identity/` holds acmd's cert and key for Remote sessions, made the first time they're needed.

The sockets are in `$TMPDIR/agent-command-<uid>` on macOS, and in `$XDG_RUNTIME_DIR/agent-command` on Linux. They are in `run/` in the folder above if that path would be too long for a socket, and on Linux when `XDG_RUNTIME_DIR` isn't set, so they are never in Linux's shared `/tmp`. acmd won't use a socket folder that is a symlink or belongs to another user. Each socket is `0600`, and the daemon and every session's host turn away a connection from another user.

## Known limits

- On macOS, if `TMPDIR` isn't set, the sockets are in `/tmp/agent-command-<uid>`. If another user made that folder first, acmd refuses it and doesn't start.
- A session's first prompt is passed to `claude` on its command line, so other users on the same Mac can see it with `ps` while the session runs.
- Bug and feature reports are built on your Mac, and the log lines in them are redacted. The report opens as a new GitHub issue in your browser, and nothing is sent until you submit it.

## Verifying releases

Each release in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases) ships `checksums.txt` with the SHA-256 of `AgentCommand.zip`. The Homebrew cask pins the same SHA-256. Releases aren't signed or notarized with an Apple Developer ID.

Each `AgentCommand.zip` also has a build provenance attestation, made by this repo's release workflow. It says which workflow run built the zip, and from which commit. To check it, download the zip from the tap release and run, with the GitHub CLI:

```sh
gh attestation verify AgentCommand.zip --repo brettmostert/agent-command
```

It finds the attestation by the zip's SHA-256, so it doesn't matter where you downloaded it from. Releases made before attestations were added have none.
