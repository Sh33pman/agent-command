package speech

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// HelperName is the speech engine's binary (helpers/acmd-speech).
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
	return "", errors.New("acmd's speech engine (acmd-speech) isn't installed next to acmd: reinstall acmd (brew reinstall agent-command)")
}

func (e *Engine) helper() (string, error) {
	if e.Helper != "" {
		return e.Helper, nil
	}
	return FindHelper()
}
