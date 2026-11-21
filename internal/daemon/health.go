package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/report"
	"github.com/brettmostert/agent-command/internal/version"
)

// Health: what's wrong with the host, said once by the daemon for every
// client, each with its fix. The setup checks (Config.CheckHealth) run at
// start, every healthEvery, and when an agent's settings file changes; the
// rest follow the daemon's state.

var (
	healthEvery      = 5 * time.Minute
	healthFilesEvery = 2 * time.Second
)

type healthState struct {
	mu    sync.Mutex
	setup []ipc.Health
	files map[string]time.Time
}

func (d *Daemon) startHealth(ctx context.Context) {
	if d.cfg.CheckHealth == nil {
		return
	}
	d.healthFilesChanged() // how the files are now: a change after this checks again
	go func() {
		d.checkHealth(ctx)
		every := time.NewTicker(healthEvery)
		files := time.NewTicker(healthFilesEvery)
		defer every.Stop()
		defer files.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-every.C:
				d.checkHealth(ctx)
			case <-files.C:
				if d.healthFilesChanged() {
					d.checkHealth(ctx)
				}
			}
		}
	}()
}

// healthFilesChanged says whether an agent's settings file changed since
// it was last looked at.
func (d *Daemon) healthFilesChanged() bool {
	h := &d.healthy
	h.mu.Lock()
	defer h.mu.Unlock()
	now := map[string]time.Time{}
	for _, f := range d.cfg.HealthFiles {
		if info, err := os.Stat(f); err == nil {
			now[f] = info.ModTime()
		}
	}
	if h.files == nil {
		h.files = now
		return false
	}
	changed := !reflect.DeepEqual(now, h.files)
	h.files = now
	return changed
}

// checkHealth runs the setup checks, and tells clients when they say
// something new.
func (d *Daemon) checkHealth(ctx context.Context) {
	got := d.cfg.CheckHealth(ctx)
	h := &d.healthy
	h.mu.Lock()
	same := reflect.DeepEqual(got, h.setup)
	h.setup = got
	h.mu.Unlock()
	if !same {
		d.changes.bump()
	}
}

// health is the list: config.toml, the setup, waking for jobs, and Hosting.
func (d *Daemon) health(sessions []*core.Session, jobs []*core.Job, wake *ipc.WakeState, remote *ipc.RemoteStatus) []ipc.Health {
	out := d.configHealth()
	d.healthy.mu.Lock()
	out = append(out, d.healthy.setup...)
	d.healthy.mu.Unlock()
	if w := wakeHealth(wake, jobs); w != nil {
		out = append(out, *w)
	}
	if r := hostingHealth(sessions, remote); r != nil {
		out = append(out, *r)
	}
	return out
}

// wakeHealth warns when scheduled jobs can't wake the Mac. nil wake: this
// daemon can't wake it at all, so there's nothing to say.
func wakeHealth(wake *ipc.WakeState, jobs []*core.Job) *ipc.Health {
	if wake == nil {
		return nil
	}
	var active, asking []*core.Job
	for _, j := range jobs {
		if !j.Paused && !j.NextRun.IsZero() {
			active = append(active, j)
			if j.Wake {
				asking = append(asking, j)
			}
		}
	}
	if len(active) == 0 {
		return nil
	}
	fix := func(title string, args ...string) *ipc.HealthFix {
		return &ipc.HealthFix{Command: append([]string{"schedule", "wake", "on"}, args...), Title: title,
			Step: "Turn on waking the Mac: macOS asks for your password once (acmd schedule wake on)."}
	}
	if wake.Allowed {
		// On, but setting the wake failed: say so, rather than only in the log.
		if wake.Error == "" || (!wake.All && len(asking) == 0) {
			return nil
		}
		return &ipc.Health{ID: ipc.HealthWake, Severity: ipc.HealthWarning, Title: "The Mac couldn't be set to wake",
			Detail: "Runs due while it sleeps may be missed. acmd said: " + wake.Error, Fix: fix("Set Up Again…")}
	}
	if len(asking) == 0 {
		return &ipc.Health{ID: ipc.HealthWake, Severity: ipc.HealthWarning, Title: "Scheduled jobs can't wake the Mac",
			Detail: "Runs due while the Mac sleeps are missed. Turning it on wakes the Mac shortly before each run.",
			Fix:    fix("Turn On…", "--all")}
	}
	names := fmt.Sprintf("%d jobs are", len(asking))
	if len(asking) == 1 {
		names = asking[0].Name + " is"
	}
	return &ipc.Health{ID: ipc.HealthWake, Severity: ipc.HealthWarning, Title: "Jobs can't wake the Mac",
		Detail: names + " set to wake the Mac, but waking it is off, so runs due while it sleeps are missed.",
		Fix:    fix("Turn On…")}
}

// hostingHealth warns when sessions are Remote but Hosting is off, so no one
// can reach them. Nothing from a daemon without Hosting.
func hostingHealth(sessions []*core.Session, remote *ipc.RemoteStatus) *ipc.Health {
	if remote == nil || remote.Enabled {
		return nil
	}
	var shared []*core.Session
	for _, s := range sessions {
		if s.Remote && !s.OnHost() && s.Status != core.StatusArchived {
			shared = append(shared, s)
		}
	}
	if len(shared) == 0 {
		return nil
	}
	which, them := fmt.Sprintf("%d sessions are Remote", len(shared)), "them"
	if len(shared) == 1 {
		which, them = sessionName(shared[0])+" is a Remote session", "it"
	}
	return &ipc.Health{ID: ipc.HealthHostingOff, Severity: ipc.HealthWarning, Title: "Hosting is off",
		Detail: which + ", but Hosting is off, so no other Agent Command can use " + them + ".",
		Fix: &ipc.HealthFix{Op: ipc.OpRemoteHosting, Data: map[string]any{"enabled": true}, Title: "Turn On Hosting",
			Step: "Turn on Hosting in Settings → Remote, or turn Remote off for " + them + "."}}
}

// features say what this host and device can do.
func (d *Daemon) features() *ipc.Features {
	agents := []string{core.HarnessClaude, core.HarnessAntigravity, core.HarnessTerminal}
	if d.cfg.Harnesses != nil {
		agents = d.cfg.Harnesses()
	}
	f := &ipc.Features{Host: ipc.HostFeatures{OS: runtime.GOOS, Agents: agents, Wake: d.cfg.Waker != nil, Hosting: d.remote != nil}}
	if d.cfg.Speech != nil {
		f.Device.Speech = d.cfg.Speech()
	}
	f.Device.Editor = d.config().Editor.Preset != ""
	return f
}

// Dismissed warnings are kept per client, by the id it gives (until clients
// register, 4.1).

func dismissedKey(clientID string) string { return "health.dismissed:" + clientID }

func (d *Daemon) dismissed(ctx context.Context, clientID string) ([]string, error) {
	if clientID == "" {
		return nil, nil
	}
	v, err := d.store.AppState(ctx, dismissedKey(clientID))
	if err != nil || v == "" {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(v), &ids); err != nil {
		return nil, nil // unreadable: shows them all again
	}
	return ids, nil
}

func (d *Daemon) setDismissed(ctx context.Context, clientID string, ids []string) error {
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := d.store.SetAppState(ctx, dismissedKey(clientID), string(b)); err != nil {
		return err
	}
	d.changes.bump()
	return nil
}

func isHealthOp(op string) bool {
	switch op {
	case ipc.OpHealthDismiss, ipc.OpHealthRestore, ipc.OpInit, ipc.OpReportDetails, ipc.OpReportPage:
		return true
	}
	return false
}

func (d *Daemon) handleHealth(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpHealthDismiss, ipc.OpHealthRestore:
		ref, err := decode[ipc.HealthRef](req.Data)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref.ClientID) == "" {
			return nil, errors.New("which client? client_id is missing")
		}
		ids := []string{}
		if req.Op == ipc.OpHealthDismiss {
			if ref.ID == "" {
				return nil, errors.New("which warning? id is missing")
			}
			if ids, err = d.dismissed(ctx, ref.ClientID); err != nil {
				return nil, err
			}
			if !slices.Contains(ids, ref.ID) {
				ids = append(ids, ref.ID)
			}
		}
		return ids, d.setDismissed(ctx, ref.ClientID, ids)
	case ipc.OpInit:
		if d.cfg.Init == nil {
			return nil, errors.New("this daemon can't set acmd up: run acmd init on the host")
		}
		res, err := d.cfg.Init(ctx)
		if d.cfg.CheckHealth != nil {
			d.checkHealth(ctx) // so the warnings it fixed go
		}
		return res, err
	case ipc.OpReportDetails, ipc.OpReportPage:
		r, err := decode[ipc.ReportRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return d.report(ctx, r, req.Op == ipc.OpReportPage)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

// report builds a report from the host's own log, cleaned of secrets: its
// details, or the GitHub page with the client's text and the details.
func (d *Daemon) report(ctx context.Context, r ipc.ReportRequest, page bool) (any, error) {
	if r.Kind != "bug" && r.Kind != "feature" {
		return nil, fmt.Errorf("a report is a bug or a feature, not %q", r.Kind)
	}
	os := "unknown"
	if d.cfg.OSVersion != nil {
		if v := d.cfg.OSVersion(ctx); v != "" {
			os = v
		}
	}
	red := report.NewRedactor()
	if !page {
		v := d.cfg.Version
		if v == "" {
			v = version.Get().Version
		}
		return ipc.ReportDetails{Details: report.Details(r.Kind, v, os, d.cfg.Paths.DaemonLog(), red)}, nil
	}
	title := strings.TrimSpace(r.Title)
	if title == "" {
		return nil, errors.New("a report needs a title")
	}
	// Everything is cleaned again here: this is the last stop before the browser.
	body := red.Text(r.Body)
	url, fits := report.IssueURL(r.Kind, red.Text(title), body)
	return ipc.ReportPage{PageURL: url, Body: body, Cut: !fits}, nil
}

// sessionName is what the apps call a session: its name, or its folder's.
func sessionName(s *core.Session) string {
	if s.Name != "" {
		return s.Name
	}
	return filepath.Base(s.Cwd)
}
