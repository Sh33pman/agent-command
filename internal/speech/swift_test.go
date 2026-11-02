package speech

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSwiftInstallAndStatus(t *testing.T) {
	e := testEngine(t)
	e.Kind = Swift
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
	if got := strings.TrimSpace(string(b)); got != "--prefetch --dir "+filepath.Join(e.Dir, "swift")+" --voice af_heart:0.6,af_bella:0.4" {
		t.Errorf("helper args %q", got)
	}
	if e.Status() != "" {
		t.Errorf("after install: %q", e.Status())
	}
	// the Python engine is separate
	py := *e
	py.Kind = Python
	if py.Status() != "not installed" {
		t.Errorf("python after swift install: %q", py.Status())
	}
	other := *e
	other.Voice = "af_sky"
	if !strings.Contains(other.Status(), "another voice") {
		t.Errorf("other voice: %q", other.Status())
	}
	if e.Socket() == py.Socket() {
		t.Error("both engines share a socket")
	}
	// a failing helper reports its output
	e.Helper = fakeHelper(t, t.TempDir(), `echo "no network" >&2; exit 3`)
	if err := e.Install(context.Background(), &out); err == nil || !strings.Contains(err.Error(), "no network") {
		t.Errorf("failed install: %v", err)
	}
}

func TestSwiftServeCommand(t *testing.T) {
	e := testEngine(t)
	e.Kind = Swift
	e.Helper = "/x/acmd-speech"
	c, err := e.serveCommand(90 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/x/acmd-speech", "--serve", e.Socket(), "--dir", filepath.Join(e.Dir, "swift"), "--voice", e.Voice, "--idle", "90"}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") {
		t.Errorf("args %q", c.Args)
	}
	if e.Name() == (&Engine{}).Name() || !strings.HasPrefix((&Engine{}).Name(), "python") {
		t.Error("names")
	}
}

func TestSwiftStartRunsHelper(t *testing.T) {
	e := testEngine(t)
	e.Kind = Swift
	e.Helper = fakeHelper(t, t.TempDir(), `echo "boom" >&2; exit 1`)
	_ = os.MkdirAll(filepath.Join(e.Dir, "swift"), 0o700)
	_ = os.WriteFile(e.marker(), []byte(`{"voice":"af_heart:0.6,af_bella:0.4"}`), 0o600)
	err := e.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), filepath.Join("swift", "engine.log")) {
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
