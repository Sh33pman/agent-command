package speech

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeHelper writes an executable script that records its arguments.
func fakeHelper(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, HelperName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindHelper(t *testing.T) {
	dir := t.TempDir()
	orig := executable
	t.Cleanup(func() { executable = orig })
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ACMD_SPEECH_HELPER", "")

	// next to acmd (dev build, release archive)
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o700)
	executable = func() (string, error) { return filepath.Join(bin, "agent-command"), nil }
	if _, err := FindHelper(); err == nil {
		t.Error("found a helper that isn't there")
	}
	// Homebrew: ../libexec
	libexec := filepath.Join(dir, "libexec")
	_ = os.MkdirAll(libexec, 0o700)
	want := fakeHelper(t, libexec, "")
	if got, err := FindHelper(); err != nil || got != want {
		t.Errorf("libexec: %q %v", got, err)
	}
	want = fakeHelper(t, bin, "")
	if got, err := FindHelper(); err != nil || got != want {
		t.Errorf("next to acmd: %q %v", got, err)
	}
	// through a symlink (brew's bin/acmd -> Cellar)
	link := filepath.Join(t.TempDir(), "acmd")
	_ = os.Symlink(filepath.Join(bin, "agent-command"), link)
	_ = os.WriteFile(filepath.Join(bin, "agent-command"), nil, 0o700)
	executable = func() (string, error) { return link, nil }
	if got, err := FindHelper(); err != nil || filepath.Base(got) != HelperName {
		t.Errorf("symlinked acmd: %q %v", got, err)
	}
	// the environment wins, and must be executable
	t.Setenv("ACMD_SPEECH_HELPER", filepath.Join(dir, "nope"))
	if _, err := FindHelper(); err == nil || !strings.Contains(err.Error(), "ACMD_SPEECH_HELPER") {
		t.Errorf("bad env: %v", err)
	}
	t.Setenv("ACMD_SPEECH_HELPER", want)
	if got, _ := FindHelper(); got != want {
		t.Errorf("env: %q", got)
	}
}

func TestInstallAndStatus(t *testing.T) {
	e := testEngine(t)
	e.Helper = ""
	t.Setenv("ACMD_SPEECH_HELPER", filepath.Join(t.TempDir(), "missing"))
	if !strings.Contains(e.Status(), "helper is missing") {
		t.Errorf("no helper: %q", e.Status())
	}
	args := filepath.Join(t.TempDir(), "args")
	e.Helper = fakeHelper(t, t.TempDir(), `echo "$@" > `+args)
	if e.Status() != "not installed" {
		t.Errorf("fresh: %q", e.Status())
	}
	var out bytes.Buffer
	if err := e.Install(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(args)
	if got := strings.TrimSpace(string(b)); got != "--prefetch --dir "+filepath.Join(e.Dir, "mlx")+" --voice af_heart:0.6,af_bella:0.4" {
		t.Errorf("helper args %q", got)
	}
	if e.Status() != "" || strings.Contains(out.String(), "old speech engine") {
		t.Errorf("after install: %q, %s", e.Status(), out.String())
	}
	other := *e
	other.Voice = "af_sky"
	if !strings.Contains(other.Status(), "another voice") {
		t.Errorf("other voice: %q", other.Status())
	}
	if e.Socket() == other.Socket() {
		t.Error("the socket doesn't follow the voice")
	}
	// a failing helper reports its output
	e.Helper = fakeHelper(t, t.TempDir(), `echo "no network" >&2; exit 3`)
	if err := e.Install(context.Background(), &out); err == nil || !strings.Contains(err.Error(), "no network") {
		t.Errorf("failed install: %v", err)
	}
}

// pythonInstall lays out what acmd before v0.32 left in the speech folder.
func pythonInstall(t *testing.T, e *Engine) {
	t.Helper()
	for _, d := range []string{"venv/bin", "venv/lib/python3.12", "hf/hub/models--mlx-community--Kokoro-82M-bf16"} {
		must(t, os.MkdirAll(filepath.Join(e.Dir, d), 0o700))
	}
	files := map[string]string{
		"venv/bin/python": "", "venv/lib/python3.12/big.so": strings.Repeat("x", 3<<20),
		"hf/hub/models--mlx-community--Kokoro-82M-bf16/model.safetensors": strings.Repeat("y", 2<<20),
		"engine.py": "#", "requirements.txt": "mlx", "engine.log": "log", "engine.pid": "4242",
		"installed.json": `{"requirements":"abc","model":"mlx-community/Kokoro-82M-bf16","voice":"af_heart"}`,
	}
	for name, body := range files {
		must(t, os.WriteFile(filepath.Join(e.Dir, name), []byte(body), 0o600))
	}
	_ = os.Symlink("/usr/bin/python3", filepath.Join(e.Dir, "venv", "bin", "python3"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPythonInstallIsSuperseded(t *testing.T) {
	e := testEngine(t)
	e.Helper = fakeHelper(t, t.TempDir(), `exit 0`)
	pythonInstall(t, e)
	// a socket of the old engine, and the current engine's
	must(t, os.WriteFile(filepath.Join(e.Runtime, "tts-0ld0ld00.sock"), nil, 0o600))
	must(t, os.WriteFile(e.Socket(), nil, 0o600))
	var killed []string
	orig := processCommand
	t.Cleanup(func() { processCommand = orig })
	processCommand = func(pid int) string {
		killed = append(killed, fmt.Sprint(pid))
		return "" // gone already: nothing is signaled
	}

	if !e.HasLegacy() || !strings.Contains(e.Status(), "older engine") {
		t.Fatalf("status with only the Python setup: %q", e.Status())
	}
	if n := e.LegacySize(); n < 5<<20 {
		t.Errorf("legacy size %d", n)
	}
	var out bytes.Buffer
	if err := e.Install(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed the old speech engine (5 MB)") {
		t.Errorf("install output: %s", out.String())
	}
	if e.HasLegacy() || e.Status() != "" {
		t.Errorf("after install: legacy %v, status %q", e.HasLegacy(), e.Status())
	}
	for _, n := range legacy {
		if _, err := os.Lstat(filepath.Join(e.Dir, n)); err == nil {
			t.Errorf("%s is still there", n)
		}
	}
	if _, err := os.Stat(e.marker()); err != nil {
		t.Error("the models' marker went too")
	}
	if _, err := os.Stat(filepath.Join(e.Runtime, "tts-0ld0ld00.sock")); err == nil {
		t.Error("the old engine's socket is still there")
	}
	if _, err := os.Stat(e.Socket()); err != nil {
		t.Error("the current engine's socket was removed")
	}
	if strings.Join(killed, ",") != "4242" {
		t.Errorf("checked pids %v", killed)
	}
}

func TestUninstallRemovesPythonInstall(t *testing.T) {
	e := testEngine(t)
	pythonInstall(t, e)
	orig := processCommand
	t.Cleanup(func() { processCommand = orig })
	processCommand = func(int) string { return "" }
	if err := e.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Dir); !os.IsNotExist(err) {
		t.Errorf("speech folder left: %v", err)
	}
}

func TestStopPIDOnlyStopsTheEngine(t *testing.T) {
	// a real process that isn't an engine: its pid is in the file, but
	// it must not be signaled
	c := exec.Command("sleep", "5")
	must(t, c.Start())
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	f := filepath.Join(t.TempDir(), "engine.pid")
	must(t, os.WriteFile(f, []byte(fmt.Sprint(c.Process.Pid)), 0o600))
	stopPID(f, "engine.py")
	time.Sleep(50 * time.Millisecond)
	if c.ProcessState != nil || c.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("a reused pid was stopped")
	}
	// it is the engine: stopped
	stopPID(f, "sleep")
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		_ = err // killed by SIGTERM
	case <-time.After(3 * time.Second):
		t.Fatal("the engine wasn't stopped")
	}
	stopPID(filepath.Join(t.TempDir(), "none"), "x") // no file: nothing happens
	if Size(5<<20) != "5 MB" || Size(3<<29) != "1.5 GB" || Size(2048) != "2 KB" {
		t.Error("sizes")
	}
}

func TestServeCommand(t *testing.T) {
	e := testEngine(t)
	e.Helper = "/x/acmd-speech"
	c, err := e.serveCommand(90 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/x/acmd-speech", "--serve", e.Socket(), "--dir", filepath.Join(e.Dir, "mlx"), "--voice", e.Voice, "--idle", "90"}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") {
		t.Errorf("args %q", c.Args)
	}
}

func TestStartRunsHelper(t *testing.T) {
	e := testEngine(t)
	e.Helper = fakeHelper(t, t.TempDir(), `echo "boom" >&2; exit 1`)
	must(t, os.MkdirAll(filepath.Join(e.Dir, "mlx"), 0o700))
	must(t, os.WriteFile(e.marker(), []byte(`{"voice":"af_heart:0.6,af_bella:0.4"}`), 0o600))
	err := e.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), filepath.Join("mlx", "engine.log")) {
		t.Fatalf("start: %v", err)
	}
	if b, _ := os.ReadFile(e.logFile()); !strings.Contains(string(b), "boom") {
		t.Errorf("log %q", b)
	}
}

func TestBench(t *testing.T) {
	e := testEngine(t)
	var texts []string
	var mu sync.Mutex
	fakeEngine(t, e.Socket(), &texts, &mu, 0)
	r, err := e.Bench(context.Background(), "One.\n\nTwo.")
	if err != nil {
		t.Fatal(err)
	}
	// 2 frames of 4 bytes = 4 samples at 24 kHz
	if r.Started || r.Audio != 166666*time.Nanosecond || r.FirstAudio <= 0 || r.Total < r.FirstAudio || r.RTF() <= 0 {
		t.Errorf("%+v", r)
	}
	if (BenchResult{}).RTF() != 0 {
		t.Error("RTF of nothing")
	}
}

func TestOnlyAmericanEnglishVoices(t *testing.T) {
	e := testEngine(t)
	e.Helper = fakeHelper(t, t.TempDir(), "exit 0")
	for _, voice := range []string{"bm_george", "af_heart:0.5,jf_alpha:0.5"} {
		e.Voice = voice
		if !strings.Contains(e.Status(), "only American English voices") {
			t.Errorf("%s: status %q", voice, e.Status())
		}
		if err := e.Install(context.Background(), &bytes.Buffer{}); err == nil {
			t.Errorf("%s: installed", voice)
		}
		if err := e.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "only American English voices") {
			t.Errorf("%s: start: %v", voice, err)
		}
	}
	e.Voice = "am_adam"
	must(t, e.Install(context.Background(), &bytes.Buffer{}))
	if e.Status() != "" {
		t.Errorf("am_adam: status %q", e.Status())
	}
}
