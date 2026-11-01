package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every acmd shortcut is the same combo in Ghostty as in Kitty: native
// actions for toggle/focus, the rest focus the sidebar and send it the
// action's private key.
func TestGhosttyBindsMatchKitty(t *testing.T) {
	conf := RenderGhosttyConf(GhosttyBinds(DefaultKeys), nil)
	for _, want := range []string{
		"keybind = cmd+shift+b=toggle_split_zoom\n",
		"keybind = cmd+shift+s=goto_split:left\n",
		"keybind = cmd+shift+n=goto_split:left\nkeybind = chain=csi:57504u\n",
		"keybind = cmd+shift+x=goto_split:left\nkeybind = chain=csi:57510u\n",
		"keybind = ctrl+cmd+9=goto_split:left\nkeybind = chain=csi:57529u\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
	kitty := map[string]bool{}
	for _, b := range KittyBinds("/x/acmd", DefaultKeys) {
		kitty[b.Key] = true
	}
	ghostty := map[string]bool{}
	for _, b := range GhosttyBinds(DefaultKeys) {
		ghostty[b.Key] = true
	}
	for k := range kitty {
		if !ghostty[k] {
			t.Errorf("%s is bound in Kitty but not in Ghostty", k)
		}
	}
	if len(kitty) != len(ghostty) {
		t.Errorf("Kitty has %d binds, Ghostty %d", len(kitty), len(ghostty))
	}

	// Opt-in: Ghostty itself accepts the file.
	if os.Getenv("ACMD_IT_GHOSTTY") == "" {
		return
	}
	f := filepath.Join(t.TempDir(), "acmd.ghostty")
	if err := os.WriteFile(f, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/Applications/Ghostty.app/Contents/MacOS/ghostty", "+validate-config", "--config-file="+f).CombinedOutput(); err != nil {
		t.Errorf("ghostty rejects it: %v\n%s", err, out)
	}
}
