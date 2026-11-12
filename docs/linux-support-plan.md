# Linux Support Plan (Debian & Arch)

This document outlines the architecture of **Agent Command** (`acmd`) and a phased plan to support Linux, targeting **Debian** and **Arch Linux**.

---

## 1. Current Architecture & Layers

| Layer | Location | Current Status on Linux |
|---|---|---|
| **Go CLI, Daemon, PTY Host & State** | `cmd/agent-command/`, `internal/` | Mostly portable (pure Go, `modernc.org/sqlite`, `creack/pty`, Unix sockets). Some `!darwin` stubs and macOS-specific calls (`open`, `osascript`, macOS paths). |
| **Terminal State Engine (`libghostty-vt`)** | `internal/vt/`, `scripts/build-ghostty.sh` | Cross-platform Zig build (`zig build -Demit-lib-vt=true`), currently gated alongside macOS-only `GhosttyKit.xcframework` and `-lc++` LDFLAGS. |
| **Native GUI App** | `app/` (`Package.swift`, `Sources/AgentCommand/`) | **macOS-only** (`.macOS(.v26)`): 36 Swift files built on AppKit, SwiftUI, Metal, Carbon, CoreVideo, IOSurface, and `GhosttyKit.xcframework`. |
| **Local TTS Speech Helper (`acmd-speech`)** | `helpers/acmd-speech/`, `internal/speech/` | **Apple Silicon-only** (`mlx-audio-swift`, `mlx-swift`, Metal shaders). Communicates with Go over a JSON-lines Unix socket. |
| **Packaging & CI** | `Makefile`, `packaging/homebrew/`, `scripts/build-app.sh`, `.github/workflows/` | Homebrew Cask + `xcodebuild` app bundle. |

---

## 2. Phase 1: Go CLI, Daemon & `libghostty-vt` on Linux

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

## 3. Phase 2: Linux GUI (Separate Frontend)

Because `app/` is deeply tied to AppKit, SwiftUI, Metal, and `GhosttyKit.xcframework`, it cannot be compiled on Linux. However, all session state, panes, inbox items, and scheduled jobs are served by the Go daemon over `daemon.sock` (`internal/ipc`).

Options for a Linux frontend talking to the same daemon:
1. **GTK4 + libadwaita** (closest to Ghostty's own Linux UI, using Ghostty or VTE for terminal surfaces).
2. **TUI** (e.g., Bubble Tea) as a lightweight, distro- and compositor-agnostic terminal UI.

---

## 4. Phase 3: Local TTS Helper (Optional)

`internal/speech` communicates with `acmd-speech` over a Unix socket (`speak` / `stop` JSON-lines protocol). On Linux, `mlx-swift` + Metal is unavailable, so `acmd-speech` can either remain optional/disabled or be backed by a compatible socket server using ONNX Runtime / `sherpa-onnx` for Kokoro-82M (avoiding GPL `espeak`, consistent with the check in [Makefile](../Makefile#L153)).

---

## 5. Phase 4: Debian & Arch Packaging

- **Debian (`.deb`):**
  - Package `agent-command` and the `acmd` symlink (e.g., via `nfpm` or `debian/`), with `libnotify-bin` as a recommended dependency.
- **Arch Linux (`PKGBUILD`):**
  - AUR package (`agent-command` / `agent-command-bin` / `agent-command-git`) with `go` and `zig` in `makedepends`, and `libnotify` in `optdepends`.
