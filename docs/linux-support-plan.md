# Linux Support Plan (Debian & Arch)

This document outlines the architecture of **Agent Command** (`acmd`) and a phased plan to support Linux, targeting **Debian** and **Arch Linux**.

---

## 1. Current Architecture & Layers

| Layer | Location | Current Status on Linux |
|---|---|---|
| **Go CLI, Daemon, PTY Host & State** | `cmd/agent-command/`, `internal/` | Builds and passes tests on Linux. Includes Protocol v2 (`internal/ipc`), op catalog (`internal/catalog`), event stream (`subscribe`), Remote sessions, and the Go TUI (`internal/tui/`). |
| **Terminal State Engine (`libghostty-vt`)** | `internal/vt/`, `scripts/build-ghostty.sh` | Cross-platform Zig build (`zig build -Demit-lib-vt=true`). Zig stays as the build tool for `libghostty-vt`; `ghostty-internal.so` is not needed once VTE replaces the Ghostty surface. |
| **macOS GUI App** | `app/` (`Package.swift`, `Sources/AgentCommand/`) | **macOS-only** (`.macOS(.v26)`): Swift, AppKit, SwiftUI, Metal, and `GhosttyKit.xcframework`. |
| **Linux GUI App** | `app-linux/` (Zig scaffold) → `cmd/agent-command-ui/` (planned Go) | Currently a minimal Zig/GTK4 scaffold (`app-linux/`). Planned switch to **Go + GTK4/libadwaita + VTE** (subject to a 3-day binding trial; Rust fallback). |
| **Local TTS Speech Helper (`acmd-speech`)** | `helpers/acmd-speech/`, `internal/speech/` | **Apple Silicon-only** (`mlx-audio-swift`, `mlx-swift`, Metal shaders). Off on Linux for now (`acmd speech status` explains why). |
| **Packaging & CI** | `Makefile`, `packaging/homebrew/`, `.github/workflows/` | CI runs on `ubuntu-latest` + `test-ui-core`; Linux release packaging (`.deb`, `PKGBUILD`, tarball) and `debian:stable` / `archlinux:latest` containers not yet wired. |

---

## 2. Phase 1: Go CLI, Daemon & `libghostty-vt` on Linux

**Status (17 Nov 2026): done.** Everything below is in `main`: PR #19 and PR #27 made the CLI, daemon, hosts and `libghostty-vt` build and run on Linux, and CI now builds and tests on Ubuntu, Debian and Arch, scans the Linux build with `govulncheck` and CodeQL, and runs the Linux UI's unit tests. Two items went another way: `editor.Opener` has no `xdg-open` fallback, and the speech helper's install hint still names Homebrew. The section is kept as a record of what was needed.

The Linux UI (Phase 2) is tracked in PAN-62, and its direction in PAN-76.

Goal: Make `acmd` (CLI, daemon, PTY hosts, Claude Code hooks, scheduled jobs, and `libghostty-vt` terminal state) build, pass tests, and run headless on Debian and Arch.

### 2.1 Build & CGo
- **[Makefile](../Makefile)**
  - Gate `speech-helper` in `make build` and `acmd-speech` / `mlx.metallib` in `make install` on `$(shell uname -s) == Darwin` so `make build` and `make install` succeed on Linux.
- **[scripts/build-ghostty.sh](../scripts/build-ghostty.sh)**
  - Gate `xcrun --show-sdk-version` and `-Demit-xcframework=true` on `uname -s == Darwin`.
  - Use `sha256sum` when `shasum` is unavailable on Linux.
  - On Linux, build only `libghostty-vt` (`zig build -Doptimize=ReleaseFast -Demit-lib-vt=true --prefix "$out/vt"`) using the pinned Zig version (`0.17.0` for Ghostty `v1.4.0`).
- **[internal/vt/vt.go](../internal/vt/vt.go)**
  - Split `#cgo LDFLAGS` by OS so Darwin links `-lc++` and Linux links `-lstdc++`:
    ```go
    #cgo darwin LDFLAGS: ${SRCDIR}/../../build/ghostty/vt/lib/libghostty-vt.a -lc++
    #cgo linux LDFLAGS: ${SRCDIR}/../../build/ghostty/vt/lib/libghostty-vt.a -lstdc++
    ```

### 2.2 Linux Platform Implementations (Replacing `!darwin` Stubs)
- **[internal/daemon/peer_other.go](../internal/daemon/peer_other.go)** (or `peer_linux.go`)
  - Implement `checkPeer(net.Conn)` using `SO_PEERCRED` (`unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)`) to verify the connecting peer's UID matches `os.Getuid()`, matching [internal/daemon/peer_darwin.go](../internal/daemon/peer_darwin.go).
- **[internal/term/front_other.go](../internal/term/front_other.go)** (or `front_linux.go`)
  - Implement `front(ptmx *os.File, shellPid int)` using `unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)` combined with `/proc/<pg>/cmdline` and `/proc/<pg>/comm` (similar to [internal/hook/proc_other.go](../internal/hook/proc_other.go)) so live agent detection works when `claude` is started inside a terminal session.

### 2.3 CLI, Notifications, Paths & Environment
- **[internal/cli/commands.go](../internal/cli/commands.go)**
  - `fallbackNotify`: Add a Linux branch using `notify-send` (`libnotify`) instead of `terminal-notifier` / `osascript`.
  - `openApp`: Guard `/usr/bin/open` behind `runtime.GOOS == "darwin"`; on Linux, launch a Linux GUI binary if installed or return a clear message directing headless users to CLI subcommands.
- **[internal/editor/editor.go](../internal/editor/editor.go)**
  - In `Opener.Argv`, guard the `/usr/bin/open -b` `preset.Bundles` fallback behind `runtime.GOOS == "darwin"` and optionally fall back to `xdg-open` on Linux.
- **[internal/paths/paths.go](../internal/paths/paths.go)**
  - Honor `XDG_STATE_HOME`, `XDG_CONFIG_HOME`, and `XDG_RUNTIME_DIR` when set (keeping the existing `~/.local/state`, `~/.config` defaults). On Debian/Arch, `XDG_RUNTIME_DIR` (`/run/user/$UID`) is already `0700` and short enough for `maxSunPath`.
- **[internal/setup/loginenv.go](../internal/setup/loginenv.go)**
  - Add Linux session variables (`DISPLAY`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`) to `LiveEnv` so stale values are not persisted in `env.json`.
  - In `CaptureLoginEnv`, fall back to `/bin/bash` or `/bin/sh` instead of `/bin/zsh` when `$SHELL` is empty on Linux.
- **[internal/doctor/doctor.go](../internal/doctor/doctor.go)**
  - Add Linux's managed settings path (`/etc/claude-code/managed-settings.json`) to `ManagedSettings`.
  - Adjust macOS-specific fix hints (e.g., `brew reinstall` in [internal/speech/helper.go](../internal/speech/helper.go)).

### 2.4 CI Verification
- Add an `ubuntu-latest` job in `.github/workflows/ci.yml` running `make lint`, `make test`, `make test-vt`, and `make build` so Linux support is continuously verified.

---

## 3. Phase 2: Linux GUI (Go + GTK4/libadwaita + VTE)

See [macos-linux-plan.html](macos-linux-plan.html) for the full platform comparison.

### 3.1 Architecture Decisions
- **Terminal widget: VTE** (instead of a Ghostty surface). Ghostty v1.4.0's embedding API (`ghostty.h`) only supports `GHOSTTY_PLATFORM_MACOS` and `GHOSTTY_PLATFORM_IOS`. VTE panes run `acmd host attach --session <id>` behind a small pane interface, with Ghostty themes applied as color palettes. Stop building/linking `ghostty-internal.so` on Linux (`Makefile`, `scripts/build-ghostty.sh`).
- **Language: Go with GTK4 and libadwaita** (e.g. `cmd/agent-command-ui/`), replacing the Zig scaffold in `app-linux/`.
  - **3-day trial first:** build a GTK4 window, a libadwaita sidebar fed by the daemon, and one VTE pane attached to a host (`gotk4` or `puregotk`). Verify typing, resizing, `Shift+Enter`, and zero leaks on Debian stable and Arch.
  - **Fallback:** Rust (`gtk4-rs`, `libadwaita-rs`, `vte4-rs`) if the Go bindings fail the trial.
- **App ID:** `com.brettmostert.agent-command` (for `GNotification` and `.desktop` integration).
- **CLI integration:** `internal/cli/ui.go` already detects `linuxUI()` on Wayland/X11 and falls back to the Go TUI (`internal/tui/`) over SSH or headless sessions.

### 3.2 Daemon v2 API & Client Contract
The Go Linux UI imports `internal/ipc`, `internal/catalog`, and `internal/core` directly instead of duplicating wire types:
- **Handshake & capabilities:** Speak `ipc.Proto` (`2`) via `Hello` (`proto_min`, `proto_max`, `ops`), and gate UI features on `Hello.Ops` rather than version numbers.
- **Client identity & notice routing:** Send `ipc.ClientHello` (`id`, `kind: "linux"`, `device`, `caps: ["notify", "attach"]`, `on_host: true`), report `client.presence` (`active`/`idle`/`background`, `focus`) and `client.profile`, and show OS notifications (`GNotification`) when `Notice.To` targets this client.
- **Event stream (`subscribe`):** Use `ipc.OpSubscribe` on a dedicated untimed stream connection (`snapshot`, then incremental `added` / `updated` / `removed` events with `rev`, plus `since` resume and `resync`) instead of polling `watch` + `snapshot`.
- **Layout & entities:** Compute sidebar/list/group layout with `core.Layout` from `Snapshot` entities (`sessions`, `groups`, `sets`, `others` for unknown entity types, `harnesses`, `view_state`), using `{host, id}` `Target`s for Remote sessions.
- **Connection pool & timeouts:** Match the macOS client (`app/Sources/AgentCommand/Daemon.swift`): keep a pool of request connections with a 30s call timeout (above the daemon's 15s Remote host wait), send `request_id` on retryable creating ops (`session.new`, `group.create`, `job.save`, `job.run`), and use cursor pagination (`inbox`, `job.runs`, `conversations`).
- **Daemon-owned ops:** Use `config.get`/`config.set`, `folders.*`, `doctor`, `init`, `report.details`/`report.page`, `editor.open`, `keys.set`, `layout`/`layout.set`, and `session.screen`.

### 3.3 Delivery Scope
1. **Trial (3 days):** Go + GTK4/libadwaita + VTE spike on Debian stable and Arch.
2. **First release (Required):** Window, sidebar, VTE panes and splits, pane sets, keymap (`Ctrl+Shift` / `Alt`), status bar, Ghostty color themes, `GNotification` notifications.
3. **Parity (Recommended):** Inbox, scheduled jobs, settings, command popup, shortcuts editor, resume conversations, bug report, Remote hosts UI.
4. **Desktop-dependent (Optional):** Reply popup (layer shell on KDE/Sway/Hyprland; normal window / notification actions on GNOME Wayland), `StatusNotifierItem` tray icon, `GlobalShortcuts` portal.

---

## 4. Phase 3: Local TTS Helper (Optional)

`internal/speech` communicates with `acmd-speech` over a Unix socket (`speak` / `stop` JSON-lines protocol). On Linux, `mlx-swift` + Metal is unavailable, so `acmd-speech` remains off initially (`acmd speech status` explains why). Later, it can be backed by a compatible socket server using ONNX Runtime / `sherpa-onnx` for Kokoro-82M (avoiding GPL `espeak`, consistent with the check in [Makefile](../Makefile#L153)).

---

## 5. Phase 4: Debian & Arch Packaging & CI

Not started. Tracked in PAN-81 (CI) and PAN-82 (first Linux release).

- **CI:** Build and test in `debian:stable` and `archlinux:latest` containers; build and test the Linux UI with GTK4/libadwaita/VTE installed; run `make vuln` and CodeQL with `GOOS=linux`.
- **Debian (`.deb`):** Package `agent-command` (`acmd` symlink), `agent-command-ui`, `.desktop` file, icon, themes, fonts, and optional systemd user service (e.g., via `nfpm`, built on Debian stable, `amd64` first), with `libnotify-bin` and GTK4/libadwaita/VTE dependencies.
- **Arch Linux (`PKGBUILD`):** AUR packages (`agent-command-bin` from release, `agent-command` from source) with `go` and `zig` in `makedepends`, and `libnotify` in `optdepends`.
- **Tarball:** Generic Linux release archive. Flatpak/Snap are not planned because sandboxing blocks PTY hosts, `~/.claude` hooks, and the user's shell/editor.
