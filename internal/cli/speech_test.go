package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/speech"
)

// speechSandbox points acmd at temporary folders with this config.toml.
func speechSandbox(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c")
	_ = os.MkdirAll(cfg, 0o700)
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	t.Setenv("ACMD_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSpeechEngineSwitch(t *testing.T) {
	speechSandbox(t, "")
	if e, err := speechEngine(); err != nil || e.Kind != speech.Python {
		t.Errorf("default: %+v %v", e, err)
	}
	speechSandbox(t, "[speech]\nengine = \"swift\"\n")
	if e, err := speechEngine(); err != nil || e.Kind != speech.Swift {
		t.Errorf("swift: %+v %v", e, err)
	}
	if e, err := speechEngineKind(speech.Python); err != nil || e.Kind != speech.Python {
		t.Errorf("override: %+v %v", e, err)
	}
	if !strings.Contains(installQuestion(&speech.Engine{Kind: speech.Swift}), "No Python") ||
		!strings.Contains(installQuestion(&speech.Engine{Kind: speech.Python}), "uv") {
		t.Error("install questions")
	}
	speechSandbox(t, "[speech]\nengine = \"rust\"\n")
	if _, err := speechEngine(); err == nil || !strings.Contains(err.Error(), "rust") {
		t.Errorf("bad engine: %v", err)
	}
}

func TestSpeechStatusShowsEngine(t *testing.T) {
	speechSandbox(t, "[speech]\nengine = \"swift\"\n")
	t.Setenv("ACMD_SPEECH_HELPER", "")
	cmd := newSpeechStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Engine: swift") || !strings.Contains(out.String(), "Core ML") {
		t.Errorf("%s", out.String())
	}
}

func TestSpeechBenchNothingSetUp(t *testing.T) {
	speechSandbox(t, "")
	cmd := newSpeechBenchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hi"})
	if err := cmd.Execute(); err == nil || !strings.Contains(out.String(), "python  not installed") {
		t.Errorf("%v\n%s", err, out.String())
	}
}
