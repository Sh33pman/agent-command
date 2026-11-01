package keymap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
)

func TestPretty(t *testing.T) {
	for in, want := range map[string]string{"cmd+shift+n": "⌘⇧N", "ctrl+cmd+1–9": "⌃⌘1–9", "shift+super+k": "⌘⇧K", "alt+x": "⌥X", "": "", "f1": "f1"} {
		if got := Pretty(in); got != want {
			t.Errorf("Pretty(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistry(t *testing.T) {
	keys := config.Default().Keys
	keys.Toggle = "cmd+b"
	keys.Editor = ""
	bs := Registry(keys, "kitty", map[string]string{"next-attention": "conflicts with Kitty's default select_all"})
	get := func(bs []Binding, action, ctx string) Binding {
		for _, b := range bs {
			if b.Action == action && b.Context == ctx {
				return b
			}
		}
		t.Fatalf("no %s/%s", ctx, action)
		return Binding{}
	}
	if b := get(bs, "toggle", Global); b.Key != "⌘B" {
		t.Errorf("user override: %+v", b)
	}
	if b := get(bs, "next-attention", Global); !strings.Contains(b.Unbound, "select_all") {
		t.Errorf("skipped: %+v", b)
	}
	if b := get(bs, "open", Global); b.Unbound != "no key configured" {
		t.Errorf("no key: %+v", b)
	}
	if b := get(bs, "go", Global); b.Key != "⌃⌘1–9" {
		t.Errorf("go: %+v", b)
	}
	// The same combos in Ghostty; its own skipped binds apply.
	gh := Registry(config.Default().Keys, "ghostty", map[string]string{"new": "conflicts in ghostty"})
	if b := get(gh, "new", Global); b.Key != "⌘⇧N" || b.Unbound != "conflicts in ghostty" {
		t.Errorf("ghostty: %+v", b)
	}
	if b := get(gh, "toggle", Global); b.Key != "⌘⇧B" {
		t.Errorf("ghostty native: %+v", b)
	}

	if n := len(Filter(bs, Groups, "", true)); n == 0 {
		t.Error("no group keys")
	}
	if n := len(Filter(bs, "", "", false)); n != len(bs)-len(Filter(bs, Groups, "", true)) {
		t.Error("groups=false keeps group keys")
	}
	if got := Filter(bs, "", "ARCH", true); len(got) != 3 {
		t.Errorf("search arch: %+v", got)
	}
	if o := Ordered(Sidebar); o[0] != Sidebar || len(o) != 4 {
		t.Errorf("Ordered: %v", o)
	}
	lines := Lines(bs, Ordered(""), 20)
	if lines[0] != "── Global ──" {
		t.Errorf("first line %q", lines[0])
	}
	for _, l := range lines {
		if len([]rune(l)) > 20 {
			t.Errorf("too wide: %q", l)
		}
	}
}

func TestWrite(t *testing.T) {
	bs := Registry(config.Default().Keys, "kitty", map[string]string{"pick": "x|y"})
	var b bytes.Buffer
	if err := Write(&b, bs, Ordered(""), "text", "kitty"); err != nil || !strings.HasPrefix(b.String(), "Shortcuts · Kitty\n\n── Global ──\n⌘⇧N") {
		t.Errorf("text: %v\n%s", err, b.String())
	}
	b.Reset()
	if err := Write(&b, bs, Ordered(""), "markdown", "kitty"); err != nil || !strings.Contains(b.String(), "| `⌘⇧N` | new session |") ||
		!strings.Contains(b.String(), "(unbound: x|y)") {
		t.Errorf("markdown: %v\n%s", err, b.String())
	}
	b.Reset()
	var back []Binding
	if err := Write(&b, bs, nil, "json", "kitty"); err != nil || json.Unmarshal(b.Bytes(), &back) != nil || len(back) != len(bs) {
		t.Errorf("json: %v", err)
	}
	b.Reset()
	if err := Write(&b, nil, nil, "json", "kitty"); err != nil || strings.TrimSpace(b.String()) != "[]" {
		t.Errorf("empty json: %q", b.String())
	}
	if err := Write(&b, bs, nil, "yaml", "kitty"); err == nil {
		t.Error("bad format accepted")
	}
}

func TestCodes(t *testing.T) {
	seen := map[rune]string{}
	for _, a := range []string{"new", "next-attention", "pick", "open", "keys", "adopt", "term", "go 1", "go 9"} {
		c, ok := Code(a)
		if !ok || c < 0xE000 || c > 0xF8FF {
			t.Errorf("%s: %U %v (not a private-use code)", a, c, ok)
		}
		if other, dup := seen[c]; dup {
			t.Errorf("%s and %s share %U", a, other, c)
		}
		seen[c] = a
		if got := Action(c); got != a {
			t.Errorf("Action(Code(%q)) = %q", a, got)
		}
	}
	for _, a := range []string{"toggle", "focus", "go 0", "go 10", "nope"} {
		if _, ok := Code(a); ok {
			t.Errorf("%s has a code", a)
		}
	}
	if Action('n') != "" || CSI(0xE0A0) != "57504u" {
		t.Errorf("Action('n') %q, CSI %q", Action('n'), CSI(0xE0A0))
	}
}
