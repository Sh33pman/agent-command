package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// touch moves the file's time on, so a change of the same size is seen.
func touch(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Duration(time.Now().UnixNano()%1000+1) * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestSetKeepsCommentsAndTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "# mine\nclaude_command = \"/bin/claude\" # pinned\n\n[notify]\n# quiet nights\ncooldown = \"20s\"\n")
	f := OpenFile(path)
	c, err := f.Set(map[string]any{"notify.cooldown": "1m", "notify.enabled": "off", "claude_args": []any{"--model", "opus"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Notify.Cooldown != "1m0s" || c.Notify.Enabled || strings.Join(c.ClaudeArgs, " ") != "--model opus" {
		t.Fatalf("settings in effect: %+v", c)
	}
	text := readFile(t, path)
	for _, want := range []string{"# mine\n", "# quiet nights\n", `cooldown = "1m0s"`, "enabled = false", `claude_args = ["--model", "opus"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
}

func TestSetIsAllOrNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\ncooldown = \"20s\"\n")
	f := OpenFile(path)
	_, err := f.Set(map[string]any{"notify.cooldown": "1m", "remote.host.port": 70000, "nope": 1})
	if err == nil || !strings.Contains(err.Error(), "remote.host.port") || !errors.Is(err, ErrUnknownSetting) {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, path); got != "[notify]\ncooldown = \"20s\"\n" {
		t.Fatalf("a refused change wrote:\n%s", got)
	}
}

func TestSetWritesOnlyWhenSomethingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\ncooldown = \"20s\"\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	f := OpenFile(path)
	if _, err := f.Set(map[string]any{"notify.cooldown": "20s"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if !info.ModTime().Equal(old) {
		t.Fatalf("an unchanged value rewrote the file at %v", info.ModTime())
	}
}

func TestSetMakesTheFileFromTheTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	f := OpenFile(path)
	if _, err := f.Set(map[string]any{"default_harness": "antigravity"}); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "# agent-command (acmd) configuration.") || !strings.Contains(text, `default_harness = "antigravity"`) {
		t.Fatalf("file:\n%s", text)
	}
	if f.Config().DefaultHarness != HarnessAntigravity {
		t.Fatal("not in effect")
	}
}

// Two changes at once, each to its own key: both land.
func TestConcurrentSetsLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\n")
	f := OpenFile(path)
	keys := []string{"notify.on_waiting", "notify.on_error", "notify.on_done", "notify.on_paused", "agent_remote_control", "remote.new_sessions_remote"}
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Go(func() {
			want := k != "agent_remote_control" && k != "remote.new_sessions_remote" && k != "notify.on_paused"
			if _, err := f.Set(map[string]any{k: !want}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Notify.OnWaiting || c.Notify.OnError || c.Notify.OnDone || !c.Notify.OnPaused || !c.AgentRemoteControl || !c.Remote.NewSessionsRemote {
		t.Fatalf("a change was lost: %+v", c)
	}
}

// A hand edit while a change is being saved: the change starts again from
// the edited file, and both are kept.
func TestAHandEditDuringASetIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\ncooldown = \"20s\"\n")
	f := OpenFile(path)
	edits := 0
	f.beforeWrite = func() {
		if edits == 0 {
			writeFile(t, path, "# by hand\n[notify]\ncooldown = \"20s\"\nsound = \"\"\n")
			touch(t, path)
		}
		edits++
	}
	c, err := f.Set(map[string]any{"notify.cooldown": "5s"})
	if err != nil {
		t.Fatal(err)
	}
	if edits != 2 {
		t.Fatalf("tried %d times, want 2", edits)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "# by hand") || !strings.Contains(text, `sound = ""`) || !strings.Contains(text, `cooldown = "5s"`) {
		t.Fatalf("file:\n%s", text)
	}
	if c.Notify.Sound != "" || c.Notify.Cooldown != "5s" {
		t.Fatalf("in effect: %+v", c.Notify)
	}
}

func TestAFileThatKeepsChangingGivesUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\n")
	f := OpenFile(path)
	n := 0
	f.beforeWrite = func() {
		n++
		writeFile(t, path, fmt.Sprintf("# edit %d\n[notify]\n", n))
		touch(t, path)
	}
	if _, err := f.Set(map[string]any{"notify.sound": ""}); err == nil || !strings.Contains(err.Error(), "try again") {
		t.Fatalf("err = %v", err)
	}
}

// A bad hand edit keeps the last good settings, says where it's wrong, and
// refuses changes until it's fixed.
func TestABadFileKeepsTheLastGoodSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "[notify]\ncooldown = \"1m\"\n")
	f := OpenFile(path)
	var told []Config
	f.OnChange(func(c Config) { told = append(told, c) })

	writeFile(t, path, "[notify]\ncooldown = \"1m\"\nsound = \n")
	touch(t, path)
	if !f.ReloadIfChanged() {
		t.Fatal("a new problem wasn't reported as a change")
	}
	p := f.Problem()
	if p == nil || p.Line != 3 || !strings.Contains(p.Error(), "line 3") {
		t.Fatalf("problem = %+v", p)
	}
	if f.Config().Notify.Cooldown != "1m" {
		t.Fatal("the last good settings weren't kept")
	}
	if len(told) != 0 {
		t.Fatal("told of a change that didn't happen")
	}
	if _, err := f.Set(map[string]any{"notify.sound": ""}); !errors.Is(err, ErrBadFile) {
		t.Fatalf("set on a bad file: %v", err)
	}
	if err := f.Edit(func(s string) (string, error) { return s, nil }); !errors.Is(err, ErrBadFile) {
		t.Fatalf("edit on a bad file: %v", err)
	}

	writeFile(t, path, "[notify]\ncooldown = \"2m\"\n")
	touch(t, path)
	if !f.ReloadIfChanged() || f.Problem() != nil {
		t.Fatal("the fix wasn't taken")
	}
	if len(told) != 1 || told[0].Notify.Cooldown != "2m" {
		t.Fatalf("told %+v", told)
	}
	if f.ReloadIfChanged() {
		t.Fatal("an unchanged file reloaded")
	}
}

func TestReadOnlySettings(t *testing.T) {
	f := OpenFile(filepath.Join(t.TempDir(), "config.toml"))
	if _, err := f.Set(map[string]any{"editor.resolved": "/bin/zed"}); err == nil {
		t.Fatal("a read-only setting was set by hand")
	}
	c, err := f.Set(map[string]any{"editor.resolved": "/bin/zed"}, AllowReadOnly)
	if err != nil || c.Editor.Resolved != "/bin/zed" {
		t.Fatalf("acmd's own: %v %+v", err, c.Editor)
	}
}

func TestOpenRetiresV1Settings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "remote_control = true\nsidebar_width = 30\n\n[keys]\nx = \"y\"\n")
	f := OpenFile(path)
	if !f.Config().AgentRemoteControl {
		t.Fatal("the old name isn't read")
	}
	if text := readFile(t, path); text != "agent_remote_control = true\n" {
		t.Fatalf("file:\n%q", text)
	}
}

func TestWatchFollowsHandEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "default_harness = \"\"\n")
	f := OpenFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	told := make(chan struct{}, 4)
	go f.Watch(ctx, 5*time.Millisecond, func() { told <- struct{}{} })
	writeFile(t, path, "default_harness = \"claude\"\n")
	touch(t, path)
	select {
	case <-told:
	case <-time.After(2 * time.Second):
		t.Fatal("the edit wasn't seen")
	}
	if f.Config().DefaultHarness != HarnessClaude {
		t.Fatal("not in effect")
	}
}

func TestEditAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	f := OpenFile(path)
	if dst, err := f.Backup("bak"); err != nil || dst != "" {
		t.Fatalf("backing up nothing: %q %v", dst, err)
	}
	writeFile(t, path, "# keep\n[notify]\nsound = \"\"\n")
	f.Reload()
	dst, err := f.Backup("bak")
	if err != nil || readFile(t, dst) != "# keep\n[notify]\nsound = \"\"\n" {
		t.Fatalf("backup %q: %v", dst, err)
	}
	if err := f.Edit(func(s string) (string, error) { return s + "x = \n", nil }); err == nil {
		t.Fatal("an edit that breaks the file was written")
	}
	if err := f.Edit(func(s string) (string, error) { return strings.Replace(s, `""`, `"default"`, 1), nil }); err != nil {
		t.Fatal(err)
	}
	if f.Config().Notify.Sound != "default" || f.Text() != "# keep\n[notify]\nsound = \"default\"\n" {
		t.Fatalf("after edit: %q", f.Text())
	}
	if f.Path() != path {
		t.Fatal(f.Path())
	}
}

func TestProblemWithoutALine(t *testing.T) {
	p := problemOf(errors.New("config.toml: something"))
	if p.Line != 0 || p.Error() != "config.toml: something" {
		t.Fatalf("%+v %q", p, p.Error())
	}
}

func TestNoPathRefusesWrites(t *testing.T) {
	f := OpenFile("")
	if _, err := f.Set(map[string]any{"notify.sound": ""}); err == nil {
		t.Fatal("wrote with no path")
	}
	if f.Config().Notify.Sound != "default" {
		t.Fatal("defaults")
	}
}
