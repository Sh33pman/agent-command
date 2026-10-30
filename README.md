# agent-command (`acmd`)

Track every Claude Code session from a sidebar in **Kitty** and **Ghostty**: live status (busy, waiting on you, done, paused, error), names, groups, notifications, and one-key navigation.

> **Status:** early development. The current release only provides `acmd version`. See [SPEC.md](SPEC.md) for the design and roadmap.

## Install (macOS)

```sh
brew install brettmostert/tap/agent-command
acmd version
```

This installs `agent-command` and the short alias `acmd`, plus shell completions. Upgrade with `brew upgrade agent-command`.

Installing by the full name, as above, trusts this formula automatically ([Homebrew Tap Trust](https://docs.brew.sh/Tap-Trust)). If you `brew tap brettmostert/tap` first and install by short name, trust it once with `brew trust --formula brettmostert/tap/agent-command`.

Before uninstalling, run `acmd uninstall` (once setup exists) to remove the config changes it makes, then `brew uninstall agent-command`.

<details>
<summary>Manual download</summary>

Universal (Apple Silicon + Intel) binaries are attached to the releases in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases). Browsers quarantine downloaded binaries, so clear the flag after extracting:

```sh
tar -xzf agent-command_<version>_darwin_all.tar.gz
xattr -d com.apple.quarantine agent-command
shasum -a 256 -c checksums.txt --ignore-missing
```
</details>

## Development

Requires Go (version from `go.mod`) and Homebrew.

```sh
make help      # list targets
make ci        # lint, test (race + coverage), vuln, build
make snapshot  # full release dry-run into ./dist
make install   # dev install to ~/.local/bin
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and release process.

## License

[MIT](LICENSE)
