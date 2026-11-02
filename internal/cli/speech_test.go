package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestSpeechEngineOldConfig(t *testing.T) {
	// configs from the Python engine still load; engine and model are ignored
	speechSandbox(t, "[speech]\nengine = \"python\"\nmodel = \"mlx-community/Kokoro-82M-bf16\"\nvoice = \"af_sky\"\n")
	e, err := speechEngine()
	if err != nil || e.Voice != "af_sky" {
		t.Errorf("old config: %+v %v", e, err)
	}
	if strings.Contains(installQuestion(e), "Python") {
		t.Error("asks about removing a Python engine that isn't there")
	}
}

func TestSpeechStatusShowsOldPythonEngine(t *testing.T) {
	dir := speechSandbox(t, "")
	t.Setenv("ACMD_SPEECH_HELPER", "/bin/sh")
	venv := filepath.Join(dir, "s", "speech", "venv", "bin")
	if err := os.MkdirAll(venv, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venv, "python"), make([]byte, 3<<20), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := newSpeechStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "Speech: set up for the old Python engine") || !strings.Contains(s, "Old Python speech engine: 3 MB") {
		t.Errorf("%s", s)
	}
	e, _ := speechEngine()
	if !strings.Contains(installQuestion(e), "old Python speech engine there (3 MB) is removed") {
		t.Errorf("install question: %s", installQuestion(e))
	}

	// uninstall removes it
	un := newSpeechUninstallCmd()
	un.SetOut(&out)
	un.SetArgs([]string{"--yes"})
	if err := un.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s", "speech")); !os.IsNotExist(err) {
		t.Errorf("speech folder left after uninstall: %v", err)
	}
}

func TestSpeechBenchNotSetUp(t *testing.T) {
	speechSandbox(t, "")
	t.Setenv("ACMD_SPEECH_HELPER", "/bin/sh")
	cmd := newSpeechBenchCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"hi"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("%v", err)
	}
}
