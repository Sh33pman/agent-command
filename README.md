# Agent Command

A native macOS app for running Claude Code sessions and terminals side by side, built on [libghostty](https://github.com/ghostty-org/ghostty). Sessions keep running when you close a pane or quit the app, and show live status: busy, waiting on you, done, paused or error.

## Features

- **Panes:** split sessions horizontally and vertically to any depth. Sessions created with splits stay together as a set.
- **Keyboard:** click a session or press ⇧⌘B to move the keyboard to the sidebar. There, the arrow keys or ⌃J and ⌃K move, X marks several sessions, Return or a double click opens one, 1 to 9 open that session, Tab goes to the next one that needs you and Esc goes back. ⌘R renames and ⇧⌘⌫ archives, acting on what's marked, the row in the sidebar, or the session you're in. Renaming several numbers them: `name - 1`, `name - 2`.
- **Going to a session:** ⌘1 to ⌘9 show a session, counting down the sidebar, from anywhere in the app. ⇧⌘A goes to the next one that needs you: waiting, then errors, then finished turns you haven't seen. ⌃⌘A brings the app forward from any app, and hides it again when you're in it.
- **Live status** in the sidebar, including an agent you start by typing `claude` in a terminal.
- **Sessions that last:** closing a pane only detaches. Only archiving ends a session, and agents can be unarchived and resumed.
- **Runs in the background:** closing the main window keeps the app running, so the popup and Dock badge still work. Click the Dock icon to bring the window back, and press ⌘Q to quit.
- **Inbox** (⇧⌘I) with what your agents have sent you: finished turns, questions, permissions and errors. Read, reply, answer or go to the session, by session, by set or as one queue.
- **Scheduled jobs** (⇧⌘J, or **Scheduled** in the status bar): run a prompt on a schedule. Choose how often (every few minutes or hours, every day, weekdays, days of the week or of the month, or once) and when, and the form writes the cron; or type cron, and the choices follow it. Either way it shows the schedule in plain English and its next runs. A job runs in a folder, in a new git worktree of a repo so it can't touch your own work, or in an existing session, continuing its conversation. Write the prompt in a Markdown editor with a toolbar, shortcuts and a preview, or point it at a Markdown file that's read at each run. Set Claude Code's model, effort, permission mode, tools, extra folders, system prompt, MCP config and settings file per job. Each run is a session like any other. Runs the Mac sleeps through show as missed, or run when it wakes if the job says so. Or have acmd wake the Mac two minutes before a run, for every job or the jobs you choose: turning it on (when the app is first set up, from the warning it shows while jobs can't wake the Mac, from **Wake the Mac** below the job list, at the end of `acmd init` in a terminal, or with `acmd schedule wake on`) asks for your password once, to add a sudoers rule (`/etc/sudoers.d/agent-command`) that lets you set and cancel acmd's own wakes, and nothing else, without one. The Mac stays awake while a woken run works. A MacBook with its lid closed on battery may not wake. `acmd schedule wake off` clears acmd's wakes and removes the rule, as does `acmd uninstall`. acmd's background service runs them, so they run with the window closed. After a restart they start once the app or a session has started the service again. From the terminal, `acmd schedule` lists, adds, edits, pauses, resumes, runs and deletes jobs (`acmd schedule add "Morning triage" --cron "0 9 * * 1-5" --prompt "Triage new issues"`), and `acmd schedule explain "<cron>"` says a schedule in English.
- **Reply popup:** when you're in another app, what your agents send opens in a window over everything, without taking your typing. Click another app and it drops behind so you can carry on; a new message brings it back. Click it or press ⌃⌘O to use it, and ⌃⌘O opens it from any app. The bell can keep it always on top over a dimmed screen instead. Move through the queue, then answer, reply or go to the session. Turn it off with ⌥⌘I or the bell in the status bar, which also chooses what opens it. While it's off, or for what it doesn't open, you get notifications instead.
- **Read aloud:** a local voice ([Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M) on MLX) reads messages in the popup, the Inbox and the agent view, with the speaker button or ⇧⌘S. Turn it on from the bell, which downloads the model (about 360 MB). The popup can read each message as it's shown. American English voices only for now.
- **Dock icon:** shows how many messages are unread, and its right-click menu opens the Inbox. Clicking it brings up the popup if that's open, otherwise the main window.
- **Notifications** when a session needs you, fails or finishes a long turn. Clicking one opens that session. After quitting with ⌘Q, they still appear, but a click can't open the session.
- **Resume** an earlier Claude conversation by ID or from a list.
- **Command popup** and a live **theme picker**, using Ghostty's themes.
- **Remote control** enabled on every Claude session it starts.
- **Report a bug or request a feature** from the Help menu or the command popup. It opens a filled-in GitHub issue in your browser for you to submit. A bug report can include the last 200 lines of the daemon log, with secrets, your account name and your home folder taken out, and you see what will be included first.
- `acmd` brings up the app, or its window if it's already running. `acmd --new-instance` starts another copy, and its subcommands list and manage sessions.

> **Status:** early development.

## Install (macOS 26 or later, Apple Silicon)

```sh
brew install --cask brettmostert/tap/agent-command
```

The install adds the app's Claude Code hooks, removing any old ones first. Agent Command isn't notarized, so the cask also clears macOS's quarantine flag for it. Run `acmd` to open it. Upgrade with `brew upgrade --cask agent-command`.

`acmd doctor` checks the setup, and `acmd doctor --fix` re-adds the hooks and replaces a stale background service.

`brew uninstall --cask agent-command` ends running sessions, stops the background service, removes the hooks and asks whether to keep your sessions and settings. `brew uninstall --zap --cask agent-command` always deletes them. Upgrades leave sessions running. After an uninstall, Claude conversations can still be resumed, and with your data kept, terminals come back as new shells in the same folders.

<details>
<summary>Manual download</summary>

Download `AgentCommand.zip` from the releases in [brettmostert/homebrew-tap](https://github.com/brettmostert/homebrew-tap/releases), then:

```sh
shasum -a 256 -c checksums.txt
unzip AgentCommand.zip
mv "Agent Command.app" /Applications/
xattr -dr com.apple.quarantine "/Applications/Agent Command.app"
"/Applications/Agent Command.app/Contents/MacOS/agent-command" init
```
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
