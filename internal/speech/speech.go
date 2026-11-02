// Package speech reads text aloud with Kokoro-82M
// (https://huggingface.co/hexgrad/Kokoro-82M), a local text-to-speech
// model (SPEC §8.9). The model runs in acmd-speech, a Swift helper shipped
// next to acmd that runs Kokoro on Core ML through FluidAudio
// (helpers/acmd-speech) and answers on a unix socket. This package
// installs it, starts it when needed, prepares text and plays what it
// makes. It also removes the Python engine (mlx-audio in a uv
// environment) that acmd used before v0.32.
package speech

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrNotInstalled means speech hasn't been set up (acmd speech install).
var ErrNotInstalled = errors.New("speech isn't set up: run `acmd speech install`")

// Engine is acmd's speech engine for one voice.
type Engine struct {
	Dir     string  // acmd's speech folder (state dir/speech)
	Runtime string  // where the engine's socket lives
	Voice   string  // voice or blend "af_heart:0.6,af_bella:0.4"
	Speed   float64 // 1.0 = normal
	// Idle is how long the engine stays loaded without requests.
	Idle time.Duration
	// Helper is the acmd-speech binary ("" = look it up).
	Helper string
}

// home holds the models. It's named "swift" from v0.31, when the Python
// and Swift engines were installed side by side; kept so those installs
// don't download the model again.
func (e *Engine) home() string    { return filepath.Join(e.Dir, "swift") }
func (e *Engine) marker() string  { return filepath.Join(e.home(), "installed.json") }
func (e *Engine) logFile() string { return filepath.Join(e.home(), "engine.log") }
func (e *Engine) pidFile() string { return filepath.Join(e.home(), "engine.pid") }

// Socket is the engine's socket; it depends on the voice, so a change
// starts a new engine (the old one stops when idle).
func (e *Engine) Socket() string {
	h := sha256.Sum256([]byte("swift\x00" + e.Voice))
	return filepath.Join(e.Runtime, "tts-"+hex.EncodeToString(h[:4])+".sock")
}

// record is what installed.json holds.
type record struct {
	Voice string `json:"voice"`
	At    string `json:"at"`
}

// Status says whether speech is set up for e's voice: "" when it is,
// otherwise what's missing.
func (e *Engine) Status() string {
	if _, err := e.helper(); err != nil {
		return "the acmd-speech helper is missing (reinstall acmd)"
	}
	b, err := os.ReadFile(e.marker())
	var r record
	if err != nil || json.Unmarshal(b, &r) != nil {
		if e.HasLegacy() {
			return "set up for the old Python engine, which acmd no longer uses: run `acmd speech install` (it also removes the Python setup)"
		}
		return "not installed"
	}
	if !sameVoices(r.Voice, e.Voice) {
		return "set up for another voice: run `acmd speech install` to download it"
	}
	return ""
}

func sameVoices(a, b string) bool {
	names := func(s string) string {
		var out []string
		for _, p := range strings.Split(s, ",") {
			n, _, _ := strings.Cut(strings.TrimSpace(p), ":")
			if n != "" {
				out = append(out, n)
			}
		}
		return strings.Join(out, ",")
	}
	return names(a) == names(b)
}

// Install downloads the Core ML model and the voices in use into e.home()
// and compiles them for this Mac (the helper's --prefetch), then removes
// the old Python engine if it's there. It writes progress to out.
func (e *Engine) Install(ctx context.Context, out io.Writer) error {
	helper, err := e.helper()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.home(), 0o700); err != nil {
		return err
	}
	fmt.Fprintln(out, "Downloading Kokoro (Core ML) and its voices and compiling them for this Mac (about 400 MB, a minute or two)…")
	c := exec.CommandContext(ctx, helper, "--prefetch", "--dir", e.home(), "--voice", e.Voice) // #nosec G204 -- acmd's own helper
	var buf strings.Builder
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Run(); err != nil {
		return fmt.Errorf("setting up speech: %w\n%s", err, lastLines(buf.String(), 15))
	}
	b, _ := json.MarshalIndent(record{Voice: e.Voice, At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err := os.WriteFile(e.marker(), b, 0o600); err != nil {
		return err
	}
	if e.HasLegacy() {
		freed, err := e.RemoveLegacy()
		if err != nil {
			return fmt.Errorf("removing the old Python speech engine: %w", err)
		}
		fmt.Fprintf(out, "Removed the old Python speech engine (%s).\n", Size(freed))
	}
	return nil
}

// legacy is what the Python engine (acmd before v0.32) kept in e.Dir.
var legacy = []string{"venv", "hf", "engine.py", "requirements.txt", "installed.json", "engine.pid", "engine.log"}

// HasLegacy says whether the old Python engine's files are there.
func (e *Engine) HasLegacy() bool {
	for _, n := range legacy {
		if _, err := os.Lstat(filepath.Join(e.Dir, n)); err == nil {
			return true
		}
	}
	return false
}

// LegacySize is how much the old Python engine's files take.
func (e *Engine) LegacySize() int64 {
	var n int64
	for _, name := range legacy {
		n += du(filepath.Join(e.Dir, name))
	}
	return n
}

// RemoveLegacy stops the old Python engine if it runs and removes its
// files (only those: the models in e.home() stay). uv's own cache and
// Python download aren't acmd's and are left alone.
func (e *Engine) RemoveLegacy() (int64, error) {
	stopPID(filepath.Join(e.Dir, "engine.pid"), "engine.py")
	// Its sockets are named after a model acmd no longer knows: remove
	// every one but the current engine's (they stop at their idle check).
	socks, _ := filepath.Glob(filepath.Join(e.Runtime, "tts-*.sock"))
	for _, s := range socks {
		if s != e.Socket() {
			_ = os.Remove(s)
		}
	}
	freed := e.LegacySize()
	for _, n := range legacy {
		if err := os.RemoveAll(filepath.Join(e.Dir, n)); err != nil {
			return 0, err
		}
	}
	return freed, nil
}

// Uninstall stops the engines and removes everything in e.Dir (the
// models, and the old Python engine if it's still there).
func (e *Engine) Uninstall() error {
	e.stopEngines()
	return os.RemoveAll(e.Dir)
}

// stopEngines asks running engines to stop by removing their sockets
// (they stop at their next idle check) and stops their processes.
func (e *Engine) stopEngines() {
	socks, _ := filepath.Glob(filepath.Join(e.Runtime, "tts-*.sock"))
	for _, s := range socks {
		_ = os.Remove(s)
	}
	stopPID(e.pidFile(), HelperName)
	stopPID(filepath.Join(e.Dir, "engine.pid"), "engine.py")
}

// processCommand is the command line of a process ("" if gone);
// swappable in tests.
var processCommand = func(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output() // #nosec G204 -- a pid
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stopPID sends SIGTERM to the process in a pid file if it's still the
// engine (its command line contains want), so a reused pid is left alone.
func stopPID(file, want string) {
	b, err := os.ReadFile(file) // #nosec G304 -- acmd's own pid files
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || !strings.Contains(processCommand(pid), want) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// du is the size of the files under p.
func du(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// Size formats a byte count for people.
func Size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// request sends one request to the engine and decodes its reply.
func (e *Engine) request(ctx context.Context, req map[string]any) (map[string]any, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", e.Socket())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var reply map[string]any
	if err := json.Unmarshal(line, &reply); err != nil {
		return nil, err
	}
	if ok, _ := reply["ok"].(bool); !ok {
		msg, _ := reply["error"].(string)
		return nil, fmt.Errorf("speech engine: %s", msg)
	}
	return reply, nil
}

// Start makes sure the engine runs (it loads the model once and stays up
// while used), waiting until it answers.
func (e *Engine) Start(ctx context.Context) error {
	if _, err := e.request(ctx, map[string]any{"op": "ping"}); err == nil {
		return nil
	}
	if st := e.Status(); st != "" {
		if st == "not installed" {
			return ErrNotInstalled
		}
		return errors.New("speech " + st)
	}
	if err := os.MkdirAll(e.Runtime, 0o700); err != nil {
		return err
	}
	idle := e.Idle
	if idle == 0 {
		idle = 10 * time.Minute
	}
	c, err := e.serveCommand(idle)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(e.logFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(e.pidFile(), []byte(fmt.Sprint(c.Process.Pid)), 0o600)
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("the speech engine stopped (see %s): %w", e.logFile(), err)
		case <-time.After(200 * time.Millisecond):
		}
		if _, err := e.request(ctx, map[string]any{"op": "ping"}); err == nil {
			return nil
		}
	}
}

// serveCommand is the command that runs the engine on e.Socket().
func (e *Engine) serveCommand(idle time.Duration) (*exec.Cmd, error) {
	helper, err := e.helper()
	if err != nil {
		return nil, err
	}
	return exec.Command(helper, "--serve", e.Socket(), "--dir", e.home(), "--voice", e.Voice, // #nosec G204 -- acmd's own helper
		"--idle", fmt.Sprint(int(idle.Seconds()))), nil
}

// Synth writes text spoken to the WAV file out and returns its length.
func (e *Engine) Synth(ctx context.Context, text, out string) (time.Duration, error) {
	speed := e.Speed
	if speed <= 0 {
		speed = 1
	}
	reply, err := e.request(ctx, map[string]any{"op": "synth", "text": text, "out": out, "speed": speed})
	if err != nil {
		return 0, err
	}
	secs, _ := reply["seconds"].(float64)
	return time.Duration(secs * float64(time.Second)), nil
}
