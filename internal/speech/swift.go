package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HelperName is the Swift engine's binary (helpers/acmd-speech).
const HelperName = "acmd-speech"

// executable is os.Executable (swappable in tests).
var executable = os.Executable

// FindHelper locates acmd-speech: $ACMD_SPEECH_HELPER, next to acmd (a
// dev build or the release archive), in ../libexec (Homebrew), then PATH.
func FindHelper() (string, error) {
	isExec := func(p string) bool {
		st, err := os.Stat(p) // #nosec G703 -- acmd's own install paths or the user's override
		return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
	}
	if p := os.Getenv("ACMD_SPEECH_HELPER"); p != "" {
		if isExec(p) {
			return p, nil
		}
		return "", fmt.Errorf("ACMD_SPEECH_HELPER=%s isn't an executable", p)
	}
	if exe, err := executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(dir, HelperName), filepath.Join(dir, "..", "libexec", HelperName)} {
			if isExec(p) {
				return filepath.Clean(p), nil
			}
		}
	}
	if p, err := exec.LookPath(HelperName); err == nil {
		return p, nil
	}
	return "", errors.New("the swift speech engine (acmd-speech) isn't installed next to acmd: reinstall acmd (brew reinstall agent-command), or set [speech] engine = \"python\"")
}

func (e *Engine) helper() (string, error) {
	if e.Helper != "" {
		return e.Helper, nil
	}
	return FindHelper()
}

func (e *Engine) swiftStatus() string {
	if _, err := e.helper(); err != nil {
		return "the acmd-speech helper is missing (reinstall acmd)"
	}
	b, err := os.ReadFile(e.marker())
	if err != nil {
		return "not installed"
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return "not installed"
	}
	if !sameVoices(r.Voice, e.Voice) {
		return "set up for another voice: run `acmd speech install` to download it"
	}
	return ""
}

// installSwift downloads the Core ML model and the voices in use into
// e.home() and compiles them for this Mac (the helper's --prefetch).
func (e *Engine) installSwift(ctx context.Context, out io.Writer) error {
	helper, err := e.helper()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.home(), 0o700); err != nil {
		return err
	}
	fmt.Fprintln(out, "Downloading Kokoro (Core ML) and its voices and compiling them for this Mac (about 400 MB, a minute or two)…")
	c := exec.CommandContext(ctx, helper, "--prefetch", "--dir", e.home(), "--voice", e.Voice) // #nosec G204 -- acmd's own helper
	var buf strings.Builder
	c.Stdout, c.Stderr = &buf, &buf
	if err := c.Run(); err != nil {
		return fmt.Errorf("setting up the swift speech engine: %w\n%s", err, lastLines(buf.String(), 15))
	}
	b, _ := json.MarshalIndent(record{Voice: e.Voice, At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return os.WriteFile(e.marker(), b, 0o600)
}
