package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Change is one file acmd creates, modifies or removes.
type Change struct {
	Path    string // absolute
	Exists  bool   // the file exists now
	Old     []byte // its current content
	New     []byte // the content to write (unused with Remove)
	Remove  bool
	Mode    os.FileMode // for new files (and always, for Secret files)
	Secret  bool        // never show the content; always enforce Mode
	Summary string
	Detail  []string // shown indented under the summary; never secrets
}

// Action is what applying the change does: create, modify, remove or
// "unchanged".
func (c Change) Action() string {
	switch {
	case c.Remove && c.Exists:
		return "remove"
	case c.Remove, c.Exists && bytes.Equal(c.Old, c.New):
		return "unchanged"
	case c.Exists:
		return "modify"
	}
	return "create"
}

// Plan is everything an init or uninstall would do.
type Plan struct {
	Changes []Change
	Notes   []string // warnings and skipped items
	After   []string // what the user should do afterwards (e.g. restart Kitty)
}

// Pending returns the changes that actually change something.
func (p *Plan) Pending() []Change {
	var out []Change
	for _, c := range p.Changes {
		if c.Action() != "unchanged" {
			out = append(out, c)
		}
	}
	return out
}

// Write prints the plan for people, with paths under home shortened to ~.
func (p *Plan) Write(w io.Writer, home string) {
	for _, c := range p.Changes {
		fmt.Fprintf(w, "  %-9s %s\n", c.Action(), Tilde(c.Path, home))
		if c.Summary != "" {
			fmt.Fprintf(w, "            %s\n", c.Summary)
		}
		if c.Action() == "unchanged" || c.Secret {
			continue
		}
		for _, d := range c.Detail {
			fmt.Fprintf(w, "              %s\n", d)
		}
	}
	if len(p.Notes) > 0 {
		fmt.Fprintln(w, "\nNotes:")
		for _, n := range p.Notes {
			fmt.Fprintf(w, "  - %s\n", n)
		}
	}
}

// Apply makes the pending changes. It first checks that no file changed
// since the plan was made, then copies every existing file it will touch
// into a new directory under backupRoot (mirroring its path under home),
// then writes each file atomically (through symlinks, so dotfile setups
// keep their links). It returns the backup directory ("" if nothing needed
// backing up).
func (p *Plan) Apply(backupRoot, home string, now time.Time) (string, error) {
	pending := p.Pending()
	for _, c := range pending {
		cur, err := os.ReadFile(c.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if exists != c.Exists || !bytes.Equal(cur, c.Old) {
			return "", fmt.Errorf("%s changed since the plan was made; nothing was changed, run the command again", Tilde(c.Path, home))
		}
	}

	backupDir := ""
	for _, c := range pending {
		if !c.Exists {
			continue
		}
		if backupDir == "" {
			var err error
			if backupDir, err = newBackupDir(backupRoot, now); err != nil {
				return "", err
			}
		}
		dst := filepath.Join(backupDir, backupName(c.Path, home))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return backupDir, err
		}
		if err := os.WriteFile(dst, c.Old, 0o600); err != nil {
			return backupDir, fmt.Errorf("back up %s: %w", c.Path, err)
		}
	}

	for _, c := range pending {
		var err error
		if c.Remove {
			err = os.Remove(c.Path)
		} else {
			err = writeAtomic(c.Path, c.New, c.Mode, c.Secret)
		}
		if err != nil {
			if backupDir != "" {
				return backupDir, fmt.Errorf("%s: %w (originals are in %s)", Tilde(c.Path, home), err, Tilde(backupDir, home))
			}
			return "", fmt.Errorf("%s: %w", Tilde(c.Path, home), err)
		}
	}
	return backupDir, nil
}

// newBackupDir creates a fresh timestamped directory, never reusing one:
// an earlier run's backups (the true originals) must not be overwritten.
func newBackupDir(root string, now time.Time) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	base := filepath.Join(root, now.UTC().Format("20060102T150405Z"))
	for i := 1; ; i++ {
		dir := base
		if i > 1 {
			dir = fmt.Sprintf("%s-%d", base, i)
		}
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrExist) || i > 100 {
			return "", err
		}
	}
}

// backupName is path relative to home, or under "root" for other paths.
func backupName(path, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Join("root", strings.TrimPrefix(path, "/"))
}

// writeAtomic writes data to path via a temporary file and rename. An
// existing file keeps its permissions unless force; a symlink is followed
// so the link itself survives.
func writeAtomic(path string, data []byte, mode os.FileMode, force bool) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		if st, err := os.Stat(target); err == nil && !force {
			mode = st.Mode().Perm()
		}
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: config directories like ~/.config/kitty are normally 0755
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".acmd-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Tilde shortens a path under home to ~/….
func Tilde(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// readOptional reads a file, treating a missing file as empty.
func readOptional(read func(string) ([]byte, error), path string) (data []byte, exists bool, err error) {
	b, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
