package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EditorPreset is a known editor.
type EditorPreset struct {
	Name     string   // config value: vscode, cursor, zed, idea
	Label    string   // shown in the picker
	Launcher string   // CLI launcher name
	Bundles  []string // macOS bundle ids (fallback launch and detection)
}

// EditorPresets are the presets, in picker order.
var EditorPresets = []EditorPreset{
	{"vscode", "VS Code", "code", []string{"com.microsoft.VSCode"}},
	{"cursor", "Cursor", "cursor", []string{"com.todesktop.230313mzl4w4u92"}},
	{"zed", "Zed", "zed", []string{"dev.zed.Zed"}},
	{"idea", "IntelliJ IDEA", "idea", []string{"com.jetbrains.intellij", "com.jetbrains.intellij.ce"}},
}

// PresetByName returns the preset called name.
func PresetByName(name string) (EditorPreset, bool) {
	for _, p := range EditorPresets {
		if p.Name == name {
			return p, true
		}
	}
	return EditorPreset{}, false
}

// DetectedEditor is a preset and what was found of it on this Mac.
type DetectedEditor struct {
	EditorPreset
	Launcher string // absolute path of the CLI launcher, "" if not found
	App      bool   // the app is installed (by bundle id)
}

// Found reports whether the editor is installed in any form.
func (d DetectedEditor) Found() bool { return d.Launcher != "" || d.App }

// DetectEditors looks for each preset's launcher (with lookPath, on the
// login-shell PATH) and app (with hasBundle, e.g. via mdfind).
func DetectEditors(lookPath func(string) (string, error), hasBundle func(id string) bool) []DetectedEditor {
	out := make([]DetectedEditor, 0, len(EditorPresets))
	for _, p := range EditorPresets {
		d := DetectedEditor{EditorPreset: p}
		if path, err := lookPath(p.Launcher); err == nil {
			d.Launcher = path
		}
		for _, id := range p.Bundles {
			if hasBundle != nil && hasBundle(id) {
				d.App = true
				break
			}
		}
		out = append(out, d)
	}
	return out
}

// LookPathIn finds an executable named file in the directories of pathList
// (a PATH value), like exec.LookPath but for another environment's PATH.
func LookPathIn(file, pathList string) (string, error) {
	if strings.Contains(file, "/") {
		if isExecutable(file) {
			return file, nil
		}
		return "", fmt.Errorf("%s: not an executable file", file)
	}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue // relative PATH entries depend on the cwd: never trust them
		}
		p := filepath.Join(dir, file)
		if isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: not found on PATH", file)
}

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// ParseCommand splits a custom editor command shell-style (quotes and
// backslashes, no expansion) into an argv template and appends "{dir}" if
// the command doesn't mention it.
func ParseCommand(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unterminated quote or escape in the command")
	}
	if inWord {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	return WithDir(args), nil
}

// WithDir appends "{dir}" to argv unless an argument already mentions it.
func WithDir(argv []string) []string {
	for _, a := range argv {
		if strings.Contains(a, "{dir}") {
			return argv
		}
	}
	return append(argv, "{dir}")
}
