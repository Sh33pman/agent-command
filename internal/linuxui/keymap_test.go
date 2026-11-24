//go:build linux

package linuxui

import (
	"path/filepath"
	"testing"
)

func TestParseKeyComboAndMatch(t *testing.T) {
	km := DefaultKeymap()

	if act, ok := km.Match(ModCtrl|ModShift, "N"); !ok || act != ActionNewSession {
		t.Fatalf("expected %s, got %q (%v)", ActionNewSession, act, ok)
	}
	if act, ok := km.Match(ModCtrl|ModShift, "T"); !ok || act != ActionNewTerminal {
		t.Fatalf("expected %s, got %q (%v)", ActionNewTerminal, act, ok)
	}
	if act, ok := km.Match(ModAlt, "3"); !ok || act != ActionSessionN(3) {
		t.Fatalf("expected %s, got %q (%v)", ActionSessionN(3), act, ok)
	}
	if c, ok := ParseKeyCombo("super+enter"); !ok || c.String() != "super+return" || c.Display() != "Super+RETURN" {
		t.Fatalf("unexpected super+enter parse: %+v (%v)", c, ok)
	}
	if c, ok := ParseKeyCombo("esc"); !ok || c.Key != "escape" {
		t.Fatalf("unexpected esc parse: %+v (%v)", c, ok)
	}
	if _, ok := km.Get(ActionQuit); !ok {
		t.Fatal("expected quit binding")
	}
	if p, err := KeymapPath(); err != nil || p == "" {
		t.Fatalf("KeymapPath: %q, %v", p, err)
	}
	if _, ok := km.Match(ModCtrl, "nonexistent"); ok {
		t.Fatal("expected unmatched key to return false")
	}
	if _, ok := ParseKeyCombo("bogus+x"); ok {
		t.Fatal("expected unknown modifier to fail")
	}
}

func TestLoadAndSaveKeymap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "linux-keys.json")

	// Missing file returns defaults.
	km, err := LoadKeymap(path)
	if err != nil {
		t.Fatalf("LoadKeymap missing: %v", err)
	}
	if err := km.Set(ActionNewTerminal, "ctrl+alt+t"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := km.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	km2, err := LoadKeymap(path)
	if err != nil {
		t.Fatalf("LoadKeymap saved: %v", err)
	}
	if act, ok := km2.Match(ModCtrl|ModAlt, "t"); !ok || act != ActionNewTerminal {
		t.Fatalf("reloaded match = %q (%v), want %s", act, ok, ActionNewTerminal)
	}
	if payload := km2.DaemonPayload(); payload["sections"] == nil {
		t.Fatal("expected sections in DaemonPayload")
	}
}
