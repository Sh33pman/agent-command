package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func healthIDs(h []ipc.Health) string {
	var ids []string
	for _, e := range h {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

func TestWakeHealth(t *testing.T) {
	next := time.Now().Add(time.Hour)
	daily := &core.Job{Name: "daily", NextRun: next}
	brief := &core.Job{Name: "brief", NextRun: next}
	brief.Wake = true
	paused := &core.Job{Name: "p", NextRun: next, Paused: true}
	if wakeHealth(nil, []*core.Job{daily}) != nil {
		t.Error("a daemon that can't wake the Mac")
	}
	if wakeHealth(&ipc.WakeState{}, []*core.Job{paused}) != nil {
		t.Error("no job that runs")
	}
	h := wakeHealth(&ipc.WakeState{}, []*core.Job{daily})
	if h == nil || h.Title != "Scheduled jobs can't wake the Mac" || strings.Join(h.Fix.Command, " ") != "schedule wake on --all" {
		t.Errorf("off, nothing asks = %+v", h)
	}
	h = wakeHealth(&ipc.WakeState{}, []*core.Job{daily, brief})
	if h == nil || !strings.HasPrefix(h.Detail, "brief is set to wake") || strings.Join(h.Fix.Command, " ") != "schedule wake on" {
		t.Errorf("off, one asks = %+v", h)
	}
	two := &core.Job{Name: "two", NextRun: next}
	two.Wake = true
	if h := wakeHealth(&ipc.WakeState{}, []*core.Job{brief, two}); h == nil || !strings.HasPrefix(h.Detail, "2 jobs are") {
		t.Errorf("off, two ask = %+v", h)
	}
	if wakeHealth(&ipc.WakeState{Allowed: true}, []*core.Job{brief}) != nil {
		t.Error("on and fine")
	}
	if wakeHealth(&ipc.WakeState{Allowed: true, Error: "pmset failed"}, []*core.Job{daily}) != nil {
		t.Error("a failure that touches no job")
	}
	if h := wakeHealth(&ipc.WakeState{Allowed: true, Error: "pmset failed"}, []*core.Job{brief}); h == nil || !strings.Contains(h.Detail, "pmset failed") {
		t.Errorf("on, but failed = %+v", h)
	}
}

func TestHostingHealth(t *testing.T) {
	api := &core.Session{Name: "api", Remote: true}
	web := &core.Session{Cwd: "/w/web", Remote: true}
	added := &core.Session{Name: "far", Remote: true, Host: "studio"}
	gone := &core.Session{Name: "old", Remote: true, Status: core.StatusArchived}
	if hostingHealth([]*core.Session{api}, nil) != nil || hostingHealth([]*core.Session{api}, &ipc.RemoteStatus{Enabled: true}) != nil {
		t.Error("no Hosting, or Hosting on")
	}
	if hostingHealth([]*core.Session{added, gone}, &ipc.RemoteStatus{}) != nil {
		t.Error("only a host's session and an archived one")
	}
	h := hostingHealth([]*core.Session{api, added}, &ipc.RemoteStatus{})
	if h == nil || !strings.HasPrefix(h.Detail, "api is a Remote session") || h.Fix.Op != ipc.OpRemoteHosting || h.Fix.Data["enabled"] != true {
		t.Errorf("one = %+v", h)
	}
	if h := hostingHealth([]*core.Session{api, web}, &ipc.RemoteStatus{}); h == nil || !strings.HasPrefix(h.Detail, "2 sessions are Remote") {
		t.Errorf("two = %+v", h)
	}
	if sessionName(web) != "web" {
		t.Error(sessionName(web))
	}
}

// The setup checks show in the snapshot, run again when an agent's settings
// file changes, and each client dismisses warnings for itself.
func TestHealthInTheSnapshot(t *testing.T) {
	p := settingsPaths(t)
	settings := filepath.Join(filepath.Dir(p.StateDir), "settings.json")
	must(t, os.WriteFile(settings, []byte("{}"), 0o600))
	var hooked atomic.Bool
	oldFiles := healthFilesEvery
	healthFilesEvery = 10 * time.Millisecond
	t.Cleanup(func() { healthFilesEvery = oldFiles })
	start(t, p, withConfig(p, func(c *Config) {
		c.HealthFiles = []string{settings}
		c.CheckHealth = func(context.Context) []ipc.Health {
			if hooked.Load() {
				return nil
			}
			return []ipc.Health{{ID: ipc.HealthHooks + "claude", Severity: ipc.HealthError, Title: "Claude Code's hooks aren't set up",
				Fix: &ipc.HealthFix{Op: ipc.OpInit}}}
		}
		c.Init = func(context.Context) (ipc.InitResult, error) {
			hooked.Store(true)
			return ipc.InitResult{Output: "Done.", Done: true}, nil
		}
	}))
	c := dial(t, p)
	snapFor := func(client string) ipc.Snapshot {
		t.Helper()
		var s ipc.Snapshot
		must(t, c.Call(ipc.OpSnapshot, ipc.SnapshotRequest{ClientID: client}, &s))
		return s
	}
	waitHealth := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if got := healthIDs(snapFor("").Health); got == want {
				return
			} else if time.Now().After(deadline) {
				t.Fatalf("health = %q, want %q", got, want)
			}
		}
	}
	waitHealth("hooks.claude")

	// Dismissed by one client, not the other.
	var ids []string
	must(t, c.Call(ipc.OpHealthDismiss, ipc.HealthRef{ClientID: "mac", ID: "hooks.claude"}, &ids))
	must(t, c.Call(ipc.OpHealthDismiss, ipc.HealthRef{ClientID: "mac", ID: "hooks.claude"}, &ids))
	if strings.Join(ids, ",") != "hooks.claude" || strings.Join(snapFor("mac").Dismissed, ",") != "hooks.claude" || snapFor("phone").Dismissed != nil {
		t.Errorf("dismissed = %v", ids)
	}
	must(t, c.Call(ipc.OpHealthRestore, ipc.HealthRef{ClientID: "mac"}, &ids))
	if len(ids) != 0 || snapFor("mac").Dismissed != nil {
		t.Errorf("restored = %v", ids)
	}
	for _, bad := range []ipc.HealthRef{{ID: "x"}, {ClientID: "mac"}} {
		if err := c.Call(ipc.OpHealthDismiss, bad, nil); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}

	// Setting up fixes it, and it goes at once.
	var res ipc.InitResult
	must(t, c.Call(ipc.OpInit, nil, &res))
	if !res.Done || res.Output != "Done." || healthIDs(snapFor("").Health) != "" {
		t.Errorf("init = %+v, health %q", res, healthIDs(snapFor("").Health))
	}
	// Losing the hooks by hand shows again, once the file changes.
	hooked.Store(false)
	later := time.Now().Add(time.Minute)
	must(t, os.Chtimes(settings, later, later))
	waitHealth("hooks.claude")

	// Features.
	f := snapFor("").Features
	if f == nil || f.Host.OS == "" || len(f.Host.HarnessKinds) == 0 || f.Host.Wake || !f.Host.Hosting || f.Device.Speech || f.Device.Editor {
		t.Errorf("features = %+v", f)
	}
	if err := c.Call(ipc.OpSnapshot, "nope", nil); err == nil {
		t.Error("took a request that isn't one")
	}
}

func TestInitAndReportOps(t *testing.T) {
	p := settingsPaths(t)
	must(t, os.MkdirAll(filepath.Dir(p.DaemonLog()), 0o700))
	start(t, p, withConfig(p, func(c *Config) {
		c.OSVersion = func(context.Context) string { return "macOS 26.1" }
	}))
	c := dial(t, p)
	if err := c.Call(ipc.OpInit, nil, nil); err == nil || !strings.Contains(err.Error(), "acmd init on the host") {
		t.Errorf("init with no Init: %v", err)
	}
	var d ipc.ReportDetails
	must(t, c.Call(ipc.OpReportDetails, ipc.ReportRequest{Kind: "feature"}, &d))
	if !strings.Contains(d.Details, testVersion) || !strings.Contains(d.Details, "on macOS 26.1") {
		t.Errorf("details = %q", d.Details)
	}
	var page ipc.ReportPage
	home, _ := os.UserHomeDir()
	must(t, c.Call(ipc.OpReportPage, ipc.ReportRequest{Kind: "bug", Title: "It broke", Body: "in " + home + "/x"}, &page))
	if !strings.HasPrefix(page.PageURL, "https://github.com/") || strings.Contains(page.Body, home) {
		t.Errorf("page = %+v", page)
	}
	for _, bad := range []ipc.ReportRequest{{Kind: "rant"}, {Kind: "bug", Body: "no title"}} {
		if err := c.Call(ipc.OpReportPage, bad, nil); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := c.Call(ipc.OpReportDetails, "nope", nil); err == nil {
		t.Error("took a request that isn't one")
	}
}

func TestNewerRelease(t *testing.T) {
	rel := []release{
		{Tag: "agent-command-v0.65.0"}, {Tag: "agent-command-v0.70.0", Draft: true}, {Tag: "agent-command-v0.71.0", Prerelease: true},
		{Tag: "speech-model-v9.0.0"}, {Tag: "agent-command-v0.64.9"}, {Tag: "agent-command-vjunk"},
	}
	if v := newerRelease("0.64.0", rel); v != "0.65.0" {
		t.Errorf("newer = %q", v)
	}
	if v := newerRelease("0.65.0", rel); v != "" {
		t.Errorf("current = %q", v)
	}
	if v := newerRelease("0.64.0+dirty", rel); v != "" {
		t.Errorf("a dev build = %q", v)
	}
	if parseVersion("v1.2.3") == nil || parseVersion("1.2") != nil || parseVersion("1.-2.3") != nil {
		t.Error("parse")
	}
}

// The daemon looks for updates when updates.check is on, and makes no
// request at all when it's off.
func TestUpdateChecks(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`[{"tag_name": "agent-command-v0.65.0"}]`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	on := &Daemon{cfg: Config{Version: "0.64.0", ReleasesURL: srv.URL, ConfigFile: filepath.Join(dir, "on.toml"), Log: log.New(io.Discard, "", 0)}}
	on.checkUpdates(context.Background())
	if u := on.update(); u.Latest != "0.65.0" || u.Current != "0.64.0" || !strings.HasSuffix(u.URL, "agent-command-v0.65.0") {
		t.Errorf("update = %+v", u)
	}
	if asked.Load() != 1 {
		t.Errorf("asked %d times", asked.Load())
	}

	// Off: nothing is asked, and nothing is said.
	off := filepath.Join(dir, "off.toml")
	must(t, writeSetting(off, "updates", "check", false))
	d := &Daemon{cfg: Config{Version: "0.64.0", ReleasesURL: srv.URL, ConfigFile: off, Log: log.New(io.Discard, "", 0)}}
	d.updates.latest = "0.65.0" // from before it was turned off
	d.checkUpdates(context.Background())
	if asked.Load() != 1 || d.update().Latest != "" {
		t.Errorf("with checks off: %d requests, update %+v", asked.Load()-1, d.update())
	}
	// A development build doesn't look at all.
	dev := &Daemon{cfg: Config{Version: "9.9.9-test", ReleasesURL: srv.URL}, stop: make(chan struct{})}
	dev.startUpdates(context.Background())
	if asked.Load() != 1 {
		t.Error("a development build looked for updates")
	}
	// A release looks at start, then every few hours, until it stops.
	running := &Daemon{cfg: Config{Version: "0.64.0", ReleasesURL: srv.URL, ConfigFile: filepath.Join(dir, "on.toml"),
		Log: log.New(io.Discard, "", 0)}, stop: make(chan struct{})}
	running.startUpdates(context.Background())
	for deadline := time.Now().Add(5 * time.Second); running.update().Latest == ""; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("it didn't look")
		}
	}
	close(running.stop)
}

func TestUpdateCheckFailures(t *testing.T) {
	var logged strings.Builder
	d := &Daemon{cfg: Config{Version: "0.64.0", Log: log.New(&logged, "", 0)}}
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) },
	} {
		srv := httptest.NewServer(h)
		d.cfg.ReleasesURL = srv.URL
		d.checkUpdates(context.Background())
		srv.Close()
	}
	d.cfg.ReleasesURL = "http://127.0.0.1:1"
	d.checkUpdates(context.Background())
	if d.update().Latest != "" || strings.Count(logged.String(), "updates:") != 3 {
		t.Errorf("logged:\n%s", logged.String())
	}
}

// fakeSpeaker is the speech engine played by the test.
type fakeSpeaker struct {
	mu        sync.Mutex
	supported bool
	installed bool
	legacy    bool
	said      []string
	stopped   int
	installs  int
	release   chan struct{} // a say waits for it, or for its stop
	failWith  error
}

func (f *fakeSpeaker) Supported() bool { return f.supported }
func (f *fakeSpeaker) Status() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installed {
		return ""
	}
	return "not installed"
}
func (f *fakeSpeaker) HasLegacy() bool { return f.legacy }
func (f *fakeSpeaker) Say(ctx context.Context, text string) error {
	f.mu.Lock()
	f.said = append(f.said, text)
	f.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-f.release:
	}
	return nil
}
func (f *fakeSpeaker) Stop(context.Context) error {
	f.mu.Lock()
	f.stopped++
	f.mu.Unlock()
	return nil
}
func (f *fakeSpeaker) Install(_ context.Context, progress func(int)) error {
	progress(50)
	progress(100)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs++
	if f.failWith != nil {
		return f.failWith
	}
	f.installed = true
	return nil
}

func TestSpeechIsADeviceOp(t *testing.T) {
	p := settingsPaths(t)
	sp := &fakeSpeaker{supported: true, release: make(chan struct{})}
	start(t, p, withConfig(p, func(c *Config) { c.Speaker = func(config.Config) Speaker { return sp } }))
	c := dial(t, p)
	var st ipc.SpeechState
	must(t, c.Call(ipc.OpSpeechStatus, nil, &st))
	if !st.Supported || st.Enabled || st.Installed {
		t.Fatalf("status = %+v", st)
	}
	if err := c.Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "speech is off") {
		t.Errorf("say while off: %v", err)
	}
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"speech.enabled": true}}, nil))
	if err := c.Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "isn't set up") {
		t.Errorf("say before setting up: %v", err)
	}
	must(t, c.Call(ipc.OpSpeechInstall, nil, &st))
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		st = ipc.SpeechState{}
		must(t, c.Call(ipc.OpSpeechStatus, nil, &st))
		if st.Installed && !st.Installing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not set up: %+v", st)
		}
	}
	if s := snapshot(t, c).Speech; s == nil || !s.Installed || !s.Enabled {
		t.Errorf("snapshot's speech = %+v", s)
	}

	// Saying answers once it's done; stopping ends it.
	done := make(chan error, 1)
	go func() { done <- dial(t, p).Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: "a long message"}, nil) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		must(t, c.Call(ipc.OpSpeechStatus, nil, &st))
		if st.Speaking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not speaking")
		}
	}
	must(t, c.Call(ipc.OpSpeechStop, nil, nil))
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a stopped say: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopping didn't end the say")
	}
	if err := c.Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: " "}, nil); err == nil {
		t.Error("said nothing")
	}
	close(sp.release)
	must(t, c.Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: "short"}, nil))
	if sp.stopped != 1 || len(sp.said) != 2 {
		t.Errorf("stopped %d, said %v", sp.stopped, sp.said)
	}
	if f := snapshot(t, c).Features; f == nil || !f.Device.Speech {
		t.Errorf("features = %+v", f)
	}
}

func TestSpeechSetupFailsAndUnsupported(t *testing.T) {
	p := settingsPaths(t)
	sp := &fakeSpeaker{supported: true, failWith: errors.New("no network"), release: make(chan struct{})}
	start(t, p, withConfig(p, func(c *Config) { c.Speaker = func(config.Config) Speaker { return sp } }))
	c := dial(t, p)
	var st ipc.SpeechState
	must(t, c.Call(ipc.OpSpeechInstall, nil, &st))
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		st = ipc.SpeechState{}
		must(t, c.Call(ipc.OpSpeechStatus, nil, &st))
		if st.Failure != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure: %+v", st)
		}
	}
	if st.Failure != "no network" || st.Installing {
		t.Errorf("failed = %+v", st)
	}
	sp.supported = false
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"speech.enabled": true}}, nil))
	if err := c.Call(ipc.OpSpeechSay, ipc.SpeechSay{Text: "hi"}, nil); err == nil || !strings.Contains(err.Error(), "isn't available") {
		t.Errorf("unsupported: %v", err)
	}
	st = ipc.SpeechState{}
	must(t, c.Call(ipc.OpSpeechInstall, nil, &st))
	if st.Installing || sp.installs != 1 {
		t.Error("set up where it can't be")
	}
	if err := c.Call(ipc.OpSpeechSay, "nope", nil); err == nil {
		t.Error("took a say that isn't one")
	}
}
