package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ACMD_CONFIG_DIR", filepath.Join(dir, "c"))
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	return filepath.Join(dir, "c")
}

func TestRemoteHelp(t *testing.T) {
	out := mustRun(t, "remote", "--help")
	for _, want := range []string{"Remote sessions", "identity", "Agent Remote Control"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestRemoteIdentity(t *testing.T) {
	config := isolate(t)
	cert := filepath.Join(config, "identity", identity.CertFile)

	out := mustRun(t, "remote", "identity")
	id, err := identity.Ensure(filepath.Join(config, "identity"))
	must(t, err)
	for _, want := range []string{"Fingerprint: " + id.Fingerprint() + "\n", "Name:        " + id.CommonName + "\n",
		"Cert:        " + cert + "\n", "Used for:    Hosting and connecting to hosts"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Asked again, it's the same identity.
	var got struct{ Fingerprint, Cert, Key string }
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "identity", "--json")), &got))
	if got.Fingerprint != id.Fingerprint() || got.Cert != cert || got.Key != id.KeyPath {
		t.Errorf("--json = %+v", got)
	}

	// Regenerating asks first.
	if _, err := acmd(t, "remote", "identity", "--regenerate"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("regenerated without confirmation: %v", err)
	}
	if again, _ := identity.Ensure(filepath.Join(config, "identity")); again.Fingerprint() != id.Fingerprint() {
		t.Error("a refused --regenerate changed the identity")
	}
	out = mustRun(t, "remote", "identity", "--regenerate", "--yes")
	if !strings.Contains(out, "The current fingerprint is "+id.Fingerprint()) || !strings.Contains(out, "Made a new identity") {
		t.Errorf("regenerate output:\n%s", out)
	}
	if strings.Contains(out, "Fingerprint: "+id.Fingerprint()) {
		t.Errorf("still the old fingerprint:\n%s", out)
	}
}

func TestRemoteIdentityRegenerate(t *testing.T) {
	config := isolate(t)
	// With no identity yet, --regenerate just makes one, without asking.
	out := mustRun(t, "remote", "identity", "--regenerate")
	if !strings.Contains(out, "Fingerprint: SHA256:") || strings.Contains(out, "Made a new identity") {
		t.Errorf("output:\n%s", out)
	}
	// A broken identity can be replaced.
	_, key := identity.Files(filepath.Join(config, "identity"))
	must(t, os.Remove(key))
	if _, err := acmd(t, "remote", "identity"); err == nil || !strings.Contains(err.Error(), "--regenerate") {
		t.Errorf("broken identity: %v", err)
	}
	out = mustRun(t, "remote", "identity", "--regenerate", "--yes")
	if !strings.Contains(out, "Made a new identity") {
		t.Errorf("output:\n%s", out)
	}
}

// writeSetting edits config.toml as a person would.
func writeSetting(path, section, key string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text, err := config.Set(string(b), section, key, v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
