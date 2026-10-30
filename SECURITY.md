# Security

## Reporting a vulnerability

Please don't open a public issue. Contact the maintainer, GitHub user `brettmostert`, privately, with a description, steps to reproduce, and the output of `acmd version`.

## Scope

agent-command runs locally. It edits your Kitty/Ghostty configuration and Claude Code settings (with backups), listens on a user-only unix socket, and controls your terminals. Of particular interest:

- Command or AppleScript injection through session/group names or folder paths
- Access to the local socket by other users
- Hooks influencing Claude Code's permission decisions (they must be observe-only)
- Sensitive data written to the state database or logs

## Verifying releases

Each release in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases) ships `checksums.txt` (SHA-256) and SBOMs. The Homebrew formula pins the archive's SHA-256.
