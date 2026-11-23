# Agent Command

A native macOS app for running Claude Code sessions and terminals side by side, built on [libghostty](https://github.com/ghostty-org/ghostty). Sessions keep running when you close a pane or quit the app, and show live status: busy, waiting on you, done, paused or error.

## Features

- **Panes:** split sessions horizontally and vertically to any depth. Sessions created with splits stay together as a set.
- **Keyboard:** click a session or press ⇧⌘B to move the keyboard to the sidebar. The sidebar, the Inbox, the popup and Scheduled share their list keys: the arrow keys, ⌃J and ⌃K, or j and k move, X marks, Return opens, Tab goes to the next that needs you (a session in the sidebar, an unread message in the Inbox), ⌫ dismisses or deletes and Esc goes back. In the sidebar, 1 to 9 open that session. ⌘R renames and ⇧⌘⌫ archives (for a session added from a Remote host, ⌥⌘⌫ removes it from this Mac and leaves it running there), acting on what's marked, the row in the sidebar, or the session you're in; in Scheduled, the same keys edit and delete the job, and ⌘W closes the Inbox or Scheduled. Renaming several numbers them: `name - 1`, `name - 2`.
- **Keyboard shortcuts** (⌘/, or **Keyboard Shortcuts** in the Help menu): every shortcut in one list, grouped by where it works. `acmd keys` prints the same list in a terminal (`--markdown` or `--json` too). Click **+** beside one and press a key to add or change it, **×** on a key to remove it, or the arrow to put back the default. It says when two actions share a key, and won't take a key that would stop a terminal or a text field getting it.
- **Settings** (⌘,): every setting in one window. **General:** open at login, the menu bar icon, Agent Remote Control for new sessions, and warnings you dismissed. **Harnesses:** what sessions and jobs run in: add, rename and remove them. Each is Claude or Antigravity with its own name, program and extra arguments; each Claude harness has its own folder (`CLAUDE_CONFIG_DIR`, with a folder picker and New Folder), so two can be signed in to different accounts. `acmd harness` does the same from a terminal. **Notifications:** what to be told about, how long a turn must take, how often per session, and the sound. **Reply Popup:** on or off, what opens it, its style, and reading aloud. **Read Aloud:** speech on or off, the voice, the speed and a sample. **Scheduled Jobs:** waking the Mac. **Appearance:** the theme, and the terminal's font and size. **Keyboard:** the shortcuts list. **Editor:** where `acmd` opens folders. **Remote:** Hosting, this Mac's identity, clients and known hosts for Remote sessions. **Advanced:** checking and repairing the setup, and the settings files. acmd's settings live in `~/.config/agent-command/config.toml`, which `acmd config show` lists and `acmd config set` changes (`acmd config set notify.cooldown 1m`). The daemon is the file's only writer and keeps its comments; a change, in the app, with `acmd` or by hand, applies straight away.
- **Menu bar icon:** opens the app, the Inbox (with how many messages are waiting), Scheduled Jobs, a new Claude session, Settings and the keyboard shortcuts, and turns the reply popup on or off, from any app, even with the window closed. Turn it off in Settings → General.
- **Going to a session:** ⌘1 to ⌘9 show a session, counting down the sidebar, from anywhere in the app. ⇧⌘A goes to the next one that needs you: waiting, then errors, then finished turns you haven't seen. ⌃⌘A brings the app forward from any app, and hides it again when you're in it.
- **Your editor:** ⇧⌘E (or **Open in Editor** in a session's menu, the File menu or the command popup) opens the session's folder in the editor chosen in Settings → Editor: VS Code, Cursor, Zed, IntelliJ IDEA or your own command. ⌘-click a file path in a terminal, such as `src/app/main.swift:42` in Claude's output, and it opens at that line, in the session's folder's window. A path is only ever opened in your editor, never run. From a terminal: `acmd open <session>` and `acmd edit <file[:line[:col]]>`.
- **Live status** in the sidebar, including an agent you start by typing `claude` in a terminal.
- **Sessions that last:** closing a pane only detaches. Only archiving ends a session, and agents can be unarchived and resumed.
- **Runs in the background:** closing the main window keeps the app running, so the popup and Dock badge still work. Click the Dock icon to bring the window back, and press ⌘Q to quit.
- **Inbox** (⇧⌘I) with what your agents have sent you: finished turns, questions, permissions and errors. Read, reply, answer or go to the session, by session, by set or as one queue.
- **Scheduled jobs** (⇧⌘J, or **Scheduled** in the status bar): run a prompt on a schedule. Choose how often (every few minutes or hours, every day, weekdays, days of the week or of the month, or once) and when, and the form writes the cron; or type cron, and the choices follow it. Either way it shows the schedule in plain English and its next runs. A job runs in a folder, in a new git worktree of a repo so it can't touch your own work, or in an existing session, continuing its conversation. Write the prompt in a Markdown editor with a toolbar, shortcuts and a preview, or point it at a Markdown file that's read at each run. Set Claude Code's model, effort, permission mode, tools, extra folders, system prompt, MCP config and settings file per job. Each run is a session like any other, with a clock beside its name in the sidebar and the Inbox (so is a session a job continues); its tooltip names the job. Runs the Mac sleeps through show as missed, or run when it wakes if the job says so. Or have acmd wake the Mac two minutes before a run, for every job or the jobs you choose: turning it on (when the app is first set up, from the warning it shows while jobs can't wake the Mac, from **Wake the Mac** below the job list, at the end of `acmd init` in a terminal, or with `acmd schedule wake on`) asks for your password once, to add a sudoers rule (`/etc/sudoers.d/agent-command`) that lets you set and cancel acmd's own wakes, and nothing else, without one. The Mac stays awake while a woken run works. A MacBook with its lid closed on battery may not wake. `acmd schedule wake off` clears acmd's wakes and removes the rule, as does `acmd uninstall`. acmd's daemon runs them, so they run with the window closed, and its service starts it at login, so they run after a restart too. From the terminal, `acmd schedule` lists, adds, edits, pauses, resumes, runs and deletes jobs (`acmd schedule add "Morning triage" --cron "0 9 * * 1-5" --prompt "Triage new issues"`), and `acmd schedule explain "<cron>"` says a schedule in English.
- **Reply popup:** when you're in another app, what your agents send opens in a window over everything, without taking your typing. Click another app and it drops behind so you can carry on; a new message brings it back. Click it or press ⌃⌘O to use it, and ⌃⌘O opens it from any app. The bell can keep it always on top over a dimmed screen instead. Move through the queue, then answer, reply or go to the session. Turn it off with ⌥⌘I or the bell in the status bar, which also chooses what opens it. While it's off, or for what it doesn't open, you get notifications instead.
- **Read aloud:** a local voice ([Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M) on MLX) reads messages in the popup, the Inbox and the agent view, with the speaker button or ⇧⌘S. Turn it on from the bell, which downloads the model (about 360 MB). The popup can read each message as it's shown. American English voices only for now.
- **Dock icon:** shows how many messages are unread, and its right-click menu opens the Inbox. Clicking it brings up the popup if that's open, otherwise the main window.
- **Notifications** when a session needs you, fails or finishes a long turn. Work Claude runs in the background counts too: when a 10-minute build it started in the background finishes, you hear about it, and until then its row says `done · background work`. Clicking one opens that session. After quitting with ⌘Q, they still appear, but a click can't open the session.
- **Resume** an earlier Claude conversation by ID or from a list.
- **Command popup** and a live **theme picker**, using Ghostty's themes.
- **Agent Remote Control:** when it's on, every Claude and Antigravity session starts with `--remote-control`, so you can use it from claude.ai, the Claude app or Antigravity. Turn it on in Settings or with `acmd agent-remote-control on` (it was `acmd remote-control`, and the `remote_control` setting is now `agent_remote_control`).
- **Remote sessions:** use the sessions that run on another Mac's Agent Command, or let another Mac use yours. See [Remote sessions](#remote-sessions) below.
- **Report a bug or request a feature** from the Help menu or the command popup. It opens a filled-in GitHub issue in your browser for you to submit. A bug report can include the last 200 lines of the daemon log, with secrets, your account name and your home folder taken out, and you see what will be included first.
- `acmd` brings up the app, or its window if it's already running. `acmd --new-instance` starts another copy, and its subcommands list and manage sessions.

> **Status:** early development.

## Remote sessions

Pair another Agent Command with this one, and it shows this Mac's sessions, groups and sets as you have them here, opens them, types in them and gets their notifications, while they keep running here. The Mac with the sessions turns on **Hosting** (Settings → Remote, or `acmd remote hosting on`), and the other adds it as a known host by its address and fingerprint (`acmd remote add`). The two connect with mutual TLS, each pinning the other's fingerprint, and the host approves each new client. Hosting is off by default, and listens only on localhost until you choose otherwise.

[docs/remote-sessions.md](docs/remote-sessions.md) has a quick start for a local network, a guide for the internet through Cloudflare Tunnel, your own certs, the security notes, every `acmd remote` command and exit code, and troubleshooting. Remote sessions are separate from Agent Remote Control, which is for claude.ai, the Claude app and Antigravity.

## Scripting with acmd

Every command that reads or changes state takes `--json`, which prints the answer of the daemon op it runs, in the daemon's wire types (`internal/ipc`), and nothing else. A command that runs several ops prints one answer per line. `acmd events` follows the daemon as it changes: a snapshot, then each session, group, inbox item, job or setting added, updated or removed, one JSON event per line (`--types` to pick, `--since` to resume). Errors go to stderr as `{"error": {"code": "…", "message": "…"}}`. Name sessions by id in scripts: the numbers `acmd list` shows follow the list's order, which changes.

`--host <name>` (or `ACMD_HOST`) runs the command on a known Remote host instead of this Mac: `acmd --host studio list --json`. A paired machine can do on the host what you can, except admin ops (purge, init, Hosting and pairing, settings that run code) and what belongs to the host's own machine (speech, waking, hooks).

| Exit code | Meaning |
| -- | -- |
| 0 | Done |
| 1 | Failed, for any reason below doesn't cover |
| 2 | Usage: the command line doesn't make sense |
| 3 | A Remote host is unreachable |
| 4 | A Remote host is waiting for its user to approve this machine |
| 5 | A Remote host refused this machine |
| 6 | A Remote host's fingerprint isn't the one you pinned |
| 7 | A Remote host speaks another protocol version |
| 8 | Hosting is on, but can't listen on its port |
| 9 | Not found: no such session, group, job or host |
| 10 | Invalid: a bad name, value or schedule |
| 11 | Not supported: the host can't do that, or a paired machine may not |
| 12 | Not running: the session's terminal has ended |
| 13 | Partial: some of several sessions didn't change |
| 14 | Conflict: it changed while acmd was saving it; run the command again |

`acmd --help` lists them too.

## Install

**The app** (macOS 26 or later, Apple Silicon):

```sh
brew install --cask brettmostert/tap/agent-command
```

**acmd on its own**, for a machine with no screen, or one you reach only from another: Linux (amd64 and arm64, any distribution), WSL2 and macOS 26 or later on Apple Silicon. On Linux and WSL2, install [Homebrew](https://brew.sh) first.

```sh
brew install brettmostert/tap/acmd
acmd init
```

`acmd init` adds the Claude Code hooks and installs the service, which runs acmd's daemon from boot on Linux and WSL2 and from login on macOS (see below). The install tries it too, but Homebrew's sandbox may stop it, so run it once yourself: it's safe to rerun. WSL2 is Linux here, and needs systemd turned on for the service. A headless Mac needs automatic login (System Settings → Users & Groups), since a launchd agent starts at login. To use the machine from another, see [A headless host](docs/remote-sessions.md#a-headless-host). Upgrade with `brew upgrade acmd`: the next acmd command restarts the daemon. Before `brew uninstall acmd`, run `acmd uninstall`.

The app runs Homebrew's acmd: the cask installs the acmd formula, and runs `acmd init`, which adds the Claude Code hooks (removing any old ones first) and the service. Agent Command isn't notarized, so the cask also clears macOS's quarantine flag for it. Run `acmd` to open it. Upgrade with `brew upgrade`: the app and acmd upgrade separately, and work together one version apart. The daemon looks for a newer release every few hours, and the app's status bar shows a dot when there is one; `acmd config set updates.check off` turns that off, and then nothing is fetched.

`acmd init` also installs the service, which runs acmd's daemon as you: a launchd agent on macOS (`~/Library/LaunchAgents/com.brettmostert.agent-command.daemon.plist`), which starts it at login, and a systemd user unit on Linux and WSL2 (`~/.config/systemd/user/agent-command.service`), which starts it at boot with lingering on. Sessions keep running when it restarts. `acmd service status` says whether it's installed and running, `acmd service install` puts it back, and `acmd service uninstall` removes it; without it, the daemon starts when something needs it. WSL2 needs systemd turned on (`[boot] systemd=true` in `/etc/wsl.conf`, then `wsl --shutdown`). `acmd daemon restart` and `acmd daemon stop` go through the service.

`acmd doctor` checks the setup, and `acmd doctor --fix` re-adds the hooks and replaces a stale daemon. `acmd doctor --json` prints the daemon's health list: the same warnings the app shows, each with its fix.

`brew uninstall --cask agent-command` removes the app only: acmd and its sessions keep running. `--zap` also removes the app's preferences and the wake rule. To remove acmd too, run `acmd uninstall` first, which ends running sessions, stops the daemon, removes its service and the hooks, and asks whether to keep your sessions and settings (`--keep-data=false` deletes them), then `brew uninstall acmd`. Homebrew has no way for a formula to do that itself. Upgrades leave sessions running. After an uninstall, Claude conversations can still be resumed, and with your data kept, terminals come back as new shells in the same folders.

<details>
<summary>Manual download</summary>

Download `AgentCommand.zip`, `acmd-<version>-darwin-arm64.tar.gz` and `checksums.txt` from the releases in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases), then:

```sh
shasum -a 256 -c --ignore-missing checksums.txt
unzip AgentCommand.zip
mv "Agent Command.app" /Applications/
xattr -dr com.apple.quarantine "/Applications/Agent Command.app"
tar -xzf acmd-*-darwin-arm64.tar.gz
# Put acmd-<version>/bin and libexec side by side somewhere on your PATH, then:
acmd init
```

The app runs `/opt/homebrew/bin/acmd`, or the acmd that opens it (`acmd`).
</details>

## Development

Requires Go (version from `go.mod`), Xcode, and the Zig version Ghostty's pinned release asks for.

```sh
make help                # list targets
make ci                  # lint, test (race + coverage), vuln, build
scripts/build-ghostty.sh # build libghostty once
make app                 # build "dist/Agent Command.app"
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and release process.

## License

[MIT](LICENSE)
