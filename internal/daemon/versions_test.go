package daemon

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/errcode"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Two clients edit the same job from what they each loaded: the first
// saves; the second is refused with conflict, not lost silently, and saves
// once it reloads. Pausing in between is a quick action: it doesn't count.
func TestEditingAJobOnTwoClientsLosesNeither(t *testing.T) {
	d, _, _, _ := jobDaemon(t)
	ctx := context.Background()
	job := mustSave(t, d, folderJob(t.TempDir()))
	if job.Version == "" {
		t.Fatal("a saved job has no version")
	}
	save := func(j any) (json.RawMessage, error) {
		data, err := json.Marshal(j)
		must(t, err)
		out, err := d.handle(ctx, ipc.Request{Op: ipc.OpJobSave, Data: data})
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	pause := func(on bool) {
		data, _ := json.Marshal(ipc.JobRef{ID: job.ID, Paused: on})
		if _, err := d.handle(ctx, ipc.Request{Op: ipc.OpJobPause, Data: data}); err != nil {
			t.Fatal(err)
		}
	}

	mac, phone := *job, *job // each as it loaded it
	mac.Name, phone.Name = "From the Mac", "From the phone"
	pause(true)
	if _, err := save(mac); err != nil {
		t.Fatalf("the first edit: %v", err)
	}
	_, err := save(phone)
	if got := resultOf("", err).Code; got != errcode.Conflict {
		t.Fatalf("the second edit, from what it loaded before the first: %v (code %s)", err, got)
	}
	now, err := d.store.Job(ctx, job.ID)
	must(t, err)
	if now.Name != "From the Mac" || !now.Paused {
		t.Errorf("after the refused edit: %q paused=%v", now.Name, now.Paused)
	}

	// The phone reloads, keeps its change, and saves.
	phone.Version = now.Version
	if _, err := save(phone); err != nil {
		t.Fatalf("after reloading: %v", err)
	}
	if now, _ = d.store.Job(ctx, job.ID); now.Name != "From the phone" || now.Version == phone.Version {
		t.Errorf("after the retry: %q %s", now.Name, now.Version)
	}
	// One with no version applies whatever it is (a script).
	plain := *now
	plain.Version, plain.Name = "", "Scripted"
	if _, err := save(plain); err != nil {
		t.Errorf("with no version: %v", err)
	}
}

// Settings are versioned one by one: a change from an older load of a key
// is refused, even when the change was made by hand in config.toml, and the
// keys it doesn't name apply whatever they are.
func TestASettingChangedSinceIsRefused(t *testing.T) {
	p := settingsPaths(t)
	start(t, p, withConfig(p))
	c := dial(t, p)
	var st ipc.ConfigState
	must(t, c.Call(ipc.OpConfigGet, nil, &st))
	loaded := maps.Clone(st.Versions) // each reply decodes into st
	if loaded["notify.on_done"] == "" || len(loaded) != len(st.Values) {
		t.Fatalf("versions = %v", loaded)
	}
	set := func(key string, v any) error {
		return c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{key: v},
			Versions: map[string]string{key: loaded[key]}}, &st)
	}
	must(t, set("notify.on_done", false))
	// Another key, still as loaded: fine.
	must(t, set("notify.on_error", false))
	// The first again, from the old load: refused.
	if err := set("notify.on_done", true); codeOf(err) != errcode.Conflict {
		t.Errorf("a stale key: %v", err)
	}
	// A hand edit counts as a change.
	must(t, writeSetting(p.Config(), "notify", "on_waiting", false))
	if err := set("notify.on_waiting", true); codeOf(err) != errcode.Conflict {
		t.Errorf("after a hand edit: %v", err)
	}
	cfg, err := config.Load(p.Config())
	must(t, err)
	if cfg.Notify.OnDone || cfg.Notify.OnWaiting {
		t.Errorf("a refused change was written: %+v", cfg.Notify)
	}
	// Without versions, it applies.
	must(t, c.Call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.on_done": true}}, nil))
}
