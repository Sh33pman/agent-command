package linuxui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func TestBuildSidebarAndFinders(t *testing.T) {
	snap := ipc.Snapshot{
		Groups: []*ipc.Group{
			{ID: "g1", Name: "Work", Order: 1},
			{ID: "default", Name: "Default", IsDefault: true},
		},
		Sessions: []*ipc.Session{
			{ID: "s1", Name: "api", GroupID: "g1", Status: core.StatusRunning},
			{ID: "s2", Name: "docs", GroupID: "default", Status: core.StatusWaiting},
		},
		Harnesses: []ipc.Harness{
			{ID: "h1", Name: "Claude Work", Kind: core.HarnessClaude, Title: "Claude", Installed: true},
		},
		ViewState: ipc.ViewState{Mode: "groups", Sort: "created"},
	}

	m := BuildSidebar(snap, "")
	if len(m.Rows) != 4 {
		t.Fatalf("expected 4 rows (2 headers + 2 sessions), got %d", len(m.Rows))
	}
	if !m.Rows[0].IsHeader() || m.Rows[1].Session.ID != "s1" || m.Rows[1].Number != 1 {
		t.Fatalf("unexpected first rows: %+v", m.Rows[:2])
	}

	filtered := BuildSidebar(snap, "docs")
	if len(filtered.Rows) != 2 || filtered.Rows[1].Session.ID != "s2" {
		t.Fatalf("unexpected filtered rows: %+v", filtered.Rows)
	}

	if h, ok := FindHarness(snap, "h1"); !ok || h.Name != "Claude Work" {
		t.Fatalf("FindHarness failed: %+v, %v", h, ok)
	}
	if _, ok := FindHarness(snap, "missing"); ok {
		t.Fatal("expected missing harness to return false")
	}

	if s, ok := FindSession(snap, "s2", ""); !ok || s.Name != "docs" {
		t.Fatalf("FindSession failed: %+v, %v", s, ok)
	}
}

func TestNoticesForClient(t *testing.T) {
	notices := []ipc.Notice{
		{Seq: 1, SessionID: "s1", Title: "old", To: []string{"linux-a"}},
		{Seq: 2, SessionID: "s2", Title: "other", To: []string{"mac-b"}},
		{Seq: 3, SessionID: "s3", Title: "mine", To: []string{"linux-a"}},
		{Seq: 4, SessionID: "s4", Title: "broadcast"},
	}

	got, maxSeq := NoticesForClient(notices, "linux-a", 1)
	if maxSeq != 4 {
		t.Fatalf("maxSeq = %d, want 4", maxSeq)
	}
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("unexpected notices: %+v", got)
	}
}

func TestAttachArgvAndStatusLabel(t *testing.T) {
	argv := AttachArgv("/usr/bin/acmd", "sess-123")
	want := []string{"/usr/bin/acmd", "host", "attach", "--session", "sess-123"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}

	if StatusLabel(core.StatusRunning) != "Running" ||
		StatusLabel(core.StatusWaiting) != "Waiting" ||
		StatusLabel(core.StatusError) != "Error" ||
		StatusLabel(core.StatusDone) != "Done" ||
		StatusLabel(core.StatusPaused) != "Paused" ||
		StatusLabel(core.StatusArchived) != "Archived" ||
		StatusLabel(core.Status("custom")) != "custom" {
		t.Fatal("unexpected status labels")
	}
}

func TestParseGhosttyTheme(t *testing.T) {
	theme := `
# comment
background = #1a1b26
foreground = #c0caf5
palette = 0=#15161e
palette = 1=#f7768e
`
	p := ParseGhosttyTheme(theme)
	bg, ok := ParseHexColor("#1a1b26")
	if !ok {
		t.Fatal("ParseHexColor failed")
	}
	if p.Background != bg {
		t.Fatalf("background = %+v, want %+v", p.Background, bg)
	}
	if _, ok := ParseHexColor("bad"); ok {
		t.Fatal("expected invalid hex to fail")
	}
}

func TestResolveACMD(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "acmd")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake acmd: %v", err)
	}
	t.Setenv("PATH", dir)
	got, err := ResolveACMD()
	if err != nil {
		t.Fatalf("ResolveACMD: %v", err)
	}
	if got != bin {
		t.Fatalf("ResolveACMD = %q, want %q", got, bin)
	}
}
