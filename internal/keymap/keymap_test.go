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

// The sheet is split into subsections, with every grouping key (sidebar
// m/v and the group-header keys) under one Sidebar / Groups heading
// (PAN-19).
func TestSections(t *testing.T) {
	bs := Registry(config.Default().Keys, "kitty", nil)
	for _, b := range bs {
		if b.Context == Sidebar || b.Context == Groups {
			if b.Section == "" {
				t.Errorf("sidebar key without a section: %+v", b)
			}
		} else if b.Section != "" {
			t.Errorf("unexpected section: %+v", b)
		}
	}
	headings := func(lines []string) []string {
		var hs []string
		for _, l := range lines {
			if strings.HasPrefix(l, "── ") {
				hs = append(hs, strings.TrimSuffix(strings.TrimPrefix(l, "── "), " ──"))
			}
		}
		return hs
	}
	want := []string{"Global", "Sidebar / Navigate", "Sidebar / Sessions", "Sidebar / Groups", "Sidebar / Filter & sort", "Sidebar / View", "Mouse"}
	lines := Lines(bs, Ordered(""), 0)
	if got := headings(lines); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("headings %q, want %q", got, want)
	}
	under := func(lines []string, heading string) string {
		var sec []string
		in := false
		for _, l := range lines {
			if strings.HasPrefix(l, "── ") {
				in = l == "── "+heading+" ──"
				continue
			}
			if in {
				sec = append(sec, l)
			}
		}
		return strings.Join(sec, "\n")
	}
	groups := under(lines, "Sidebar / Groups")
	for _, k := range []string{"move to group", "groups ↔ list", "collapse · expand", "focus group", "delete group"} {
		if !strings.Contains(groups, k) {
			t.Errorf("Sidebar / Groups lacks %q:\n%s", k, groups)
		}
	}
	if f := under(lines, "Sidebar / Filter & sort"); !strings.Contains(f, "filter (g:group)") || strings.Contains(f, "group up") {
		t.Errorf("Filter section:\n%s", f)
	}

	// The sidebar's own sheet lists its sections first; without groups the
	// Groups section keeps only m and v.
	sb := Lines(Filter(bs, "", "", false), Ordered(Sidebar), 0)
	if hs := headings(sb); hs[0] != "Sidebar / Navigate" || hs[len(hs)-2] != "Global" {
		t.Errorf("sidebar order %q", hs)
	}
	if g := under(sb, "Sidebar / Groups"); strings.Contains(g, "delete group") || !strings.Contains(g, "move to group") {
		t.Errorf("groups without groups:\n%s", g)
	}
	// --context groups: only the header keys, still under Sidebar / Groups.
	if hs := headings(Lines(Filter(bs, Groups, "", true), Ordered(Groups), 0)); len(hs) != 1 || hs[0] != "Sidebar / Groups" {
		t.Errorf("groups context %q", hs)
	}
	// Searching a section name finds its keys.
	if got := Filter(bs, "", "filter & sort", true); len(got) != 3 {
		t.Errorf("search section: %+v", got)
	}
	// Narrow sidebars clip headings too.
	for _, l := range Lines(bs, Ordered(Sidebar), 12) {
		if len([]rune(l)) > 12 {
			t.Errorf("too wide: %q", l)
		}
	}
	if testing.Verbose() {
		t.Log("\n" + strings.Join(lines, "\n"))
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
