// Package speech reads text aloud with a local MLX text-to-speech model
// (Kokoro by default; SPEC §8.9). The model runs in acmd's speech engine
// (assets/engine.py, mlx-audio in a private Python environment that
// `acmd speech install` sets up with uv); this package installs it,
// starts it when needed, prepares text and plays what it makes.
package speech

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed assets/engine.py assets/requirements.txt
var assets embed.FS

// ErrNotInstalled means speech hasn't been set up (acmd speech install).
var ErrNotInstalled = errors.New("speech isn't set up: run `acmd speech install`")

// Engine is acmd's speech engine for one model and voice.
type Engine struct {
	Dir     string  // where the environment and model live
	Runtime string  // where the engine's socket lives
	Model   string  // Hugging Face repo of an MLX TTS model
	Voice   string  // voice or blend "af_heart:0.6,af_bella:0.4"
	Speed   float64 // 1.0 = normal
	// Idle is how long the engine stays loaded without requests.
	Idle time.Duration
	// UV is the uv binary ("" = look it up).
	UV string
}

func (e *Engine) python() string { return filepath.Join(e.Dir, "venv", "bin", "python") }
func (e *Engine) script() string { return filepath.Join(e.Dir, "engine.py") }
func (e *Engine) marker() string { return filepath.Join(e.Dir, "installed.json") }
func (e *Engine) env() []string {
	return append(os.Environ(), "HF_HOME="+filepath.Join(e.Dir, "hf"), "HF_HUB_DISABLE_PROGRESS_BARS=1",
		"TOKENIZERS_PARALLELISM=false")
}

// Socket is the engine's socket; it depends on the model and voice, so a
// change starts a new engine (the old one stops when idle).
func (e *Engine) Socket() string {
	h := sha256.Sum256([]byte(e.Model + "\x00" + e.Voice))
	return filepath.Join(e.Runtime, "tts-"+hex.EncodeToString(h[:4])+".sock")
}

// record is what installed.json holds.
type record struct {
	Requirements string `json:"requirements"` // sha256 of requirements.txt
	Model        string `json:"model"`
	Voice        string `json:"voice"`
	At           string `json:"at"`
}

func requirementsHash() string {
	b, _ := assets.ReadFile("assets/requirements.txt")
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Status says whether speech is set up for e's model and voice: "" when
// it is, otherwise what's missing.
func (e *Engine) Status() string {
	b, err := os.ReadFile(e.marker())
	if err != nil {
		return "not installed"
	}
	var r record
	if json.Unmarshal(b, &r) != nil || r.Requirements != requirementsHash() {
		return "installed for an older acmd: run `acmd speech install` again"
	}
	if _, err := os.Stat(e.python()); err != nil {
		return "its Python environment is missing: run `acmd speech install`"
	}
	if r.Model != e.Model || !sameVoices(r.Voice, e.Voice) {
		return "set up for another model or voice: run `acmd speech install` to download it"
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

// FindUV locates uv: on PATH, then its usual install places.
func FindUV() (string, error) {
	if p, err := exec.LookPath("uv"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "uv"), "/opt/homebrew/bin/uv", "/usr/local/bin/uv", filepath.Join(home, ".cargo", "bin", "uv")} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("speech needs uv (Python package manager) to set itself up: install it with `brew install uv` and run `acmd speech install` again")
}

// Install sets up the engine: a Python 3.12 environment with mlx-audio
// (pinned versions) in e.Dir, and the model with only the voices in use.
// It writes progress to out. Nothing outside e.Dir changes except uv's own
// cache and Python downloads.
func (e *Engine) Install(ctx context.Context, out io.Writer) error {
	uv := e.UV
	if uv == "" {
		var err error
		if uv, err = FindUV(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(e.Dir, 0o700); err != nil {
		return err
	}
	if err := e.writeAssets(); err != nil {
		return err
	}
	run := func(what string, name string, args ...string) error {
		fmt.Fprintf(out, "%s…\n", what)
		c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- uv and acmd's own environment
		c.Env = e.env()
		var buf strings.Builder
		c.Stdout, c.Stderr = &buf, &buf
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s: %w\n%s", strings.ToLower(what), err, lastLines(buf.String(), 15))
		}
		return nil
	}
	if _, err := os.Stat(e.python()); err != nil {
		if err := run("Creating a Python 3.12 environment", uv, "venv", "--python", "3.12", filepath.Join(e.Dir, "venv")); err != nil {
			return err
		}
	}
	if err := run("Installing mlx-audio and its dependencies (about 600 MB)", uv, "pip", "install", "--python", e.python(),
		"-r", filepath.Join(e.Dir, "requirements.txt")); err != nil {
		return err
	}
	if err := run("Downloading "+e.Model+" and its voices (about 320 MB)", e.python(), e.script(),
		"--prefetch", "--model", e.Model, "--voice", e.Voice); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(record{Requirements: requirementsHash(), Model: e.Model, Voice: e.Voice,
		At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return os.WriteFile(e.marker(), b, 0o600)
}

// writeAssets writes the engine script and the requirements (the script
// is rewritten before every start, so an upgraded acmd uses its own).
func (e *Engine) writeAssets() error {
	for _, name := range []string{"engine.py", "requirements.txt"} {
		b, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(e.Dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall stops the engine and removes everything in e.Dir.
func (e *Engine) Uninstall() error {
	e.stopEngines()
	return os.RemoveAll(e.Dir)
}

// stopEngines asks running engines to stop by removing their sockets
// (they stop at their next idle check) and kills their processes.
func (e *Engine) stopEngines() {
	socks, _ := filepath.Glob(filepath.Join(e.Runtime, "tts-*.sock"))
	for _, s := range socks {
		_ = os.Remove(s)
	}
	if b, err := os.ReadFile(filepath.Join(e.Dir, "engine.pid")); err == nil {
		var pid int
		if _, err := fmt.Sscan(string(b), &pid); err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
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
	if err := e.writeAssets(); err != nil {
		return err
	}
	if err := os.MkdirAll(e.Runtime, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(e.Dir, "engine.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	idle := e.Idle
	if idle == 0 {
		idle = 10 * time.Minute
	}
	c := exec.Command(e.python(), e.script(), "--serve", e.Socket(), "--model", e.Model, "--voice", e.Voice, // #nosec G204 -- acmd's own environment
		"--idle", fmt.Sprint(int(idle.Seconds())))
	c.Env = append(e.env(), "HF_HUB_OFFLINE=1")
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(e.Dir, "engine.pid"), []byte(fmt.Sprint(c.Process.Pid)), 0o600)
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("the speech engine stopped (see %s): %w", filepath.Join(e.Dir, "engine.log"), err)
		case <-time.After(200 * time.Millisecond):
		}
		if _, err := e.request(ctx, map[string]any{"op": "ping"}); err == nil {
			return nil
		}
	}
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
