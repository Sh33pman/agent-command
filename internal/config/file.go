package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// File is config.toml as the daemon keeps it: the only thing that writes it.
// It holds the settings in effect, follows hand edits, and changes values in
// the file as it is on disk at that moment, so neither a hand edit nor
// another change is lost. A file that doesn't parse leaves the last good
// settings in effect, and refuses changes until it's fixed.
type File struct {
	path string

	mu       sync.Mutex // reading and writing the file
	cur      Config
	problem  *Problem
	stamp    stamp // the file when last read
	onChange []func(Config)

	// beforeWrite runs between reading the file and replacing it: tests edit
	// the file there, as a person might.
	beforeWrite func()
}

// Problem is what's wrong with config.toml on disk.
type Problem struct {
	Line    int    `json:"line,omitempty"` // 1-based; 0 when unknown
	Message string `json:"message"`
}

func (p *Problem) Error() string {
	if p.Line > 0 {
		return fmt.Sprintf("config.toml, line %d: %s", p.Line, p.Message)
	}
	return "config.toml: " + p.Message
}

// ErrBadFile is a change refused because config.toml doesn't parse.
var ErrBadFile = errors.New("config.toml has a mistake: fix it first")

// stamp tells whether the file changed: its modification time and size.
// text is what it held when last read.
type stamp struct {
	mod    time.Time
	size   int64
	exists bool
	text   string
}

func (s stamp) same(o stamp) bool {
	return s.exists == o.exists && s.mod.Equal(o.mod) && s.size == o.size
}

// OpenFile reads config.toml at path (a missing file is the defaults). It
// takes acmd v1's settings out of the file (Retire) when they're there.
func OpenFile(path string) *File {
	f := &File{path: path, cur: Default()}
	f.mu.Lock()
	f.loadLocked()
	if f.problem == nil && f.stamp.exists {
		if text, err := Retire(f.stamp.text); err == nil && text != f.stamp.text {
			_ = f.writeLocked(text)
		}
	}
	f.mu.Unlock()
	return f
}

// Path is where config.toml is.
func (f *File) Path() string { return f.path }

// Config is the settings in effect.
func (f *File) Config() Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

// Problem is what's wrong with the file on disk, or nil.
func (f *File) Problem() *Problem {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.problem == nil {
		return nil
	}
	p := *f.problem
	return &p
}

// OnChange calls fn with the new settings after they change, by a change
// here or by hand.
func (f *File) OnChange(fn func(Config)) {
	f.mu.Lock()
	f.onChange = append(f.onChange, fn)
	f.mu.Unlock()
}

// ReloadIfChanged reads the file again when it changed on disk, and says
// whether the settings or the problem changed.
func (f *File) ReloadIfChanged() bool {
	f.mu.Lock()
	now := f.statLocked()
	if now.same(f.stamp) {
		f.mu.Unlock()
		return false
	}
	return f.reloadAndTell()
}

// Reload reads the file again, and says whether the settings or the problem
// changed.
func (f *File) Reload() bool {
	f.mu.Lock()
	return f.reloadAndTell()
}

// reloadAndTell reloads (f.mu is held, and released here), then tells the
// OnChange funcs when the settings changed.
func (f *File) reloadAndTell() bool {
	before, hadProblem := f.cur, f.problem
	f.loadLocked()
	changed := !equalConfig(before, f.cur)
	problemChanged := (hadProblem == nil) != (f.problem == nil) ||
		(hadProblem != nil && f.problem != nil && *hadProblem != *f.problem)
	cur, fns := f.cur, slices.Clone(f.onChange)
	f.mu.Unlock()
	if changed {
		for _, fn := range fns {
			fn(cur)
		}
	}
	return changed || problemChanged
}

// Watch reloads the file whenever it changes on disk, checking every
// interval, until ctx is done. tell is called after each change (it may be
// nil).
func (f *File) Watch(ctx context.Context, every time.Duration, tell func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if f.ReloadIfChanged() && tell != nil {
				tell()
			}
		}
	}
}

// SetOption changes what Set allows.
type SetOption int

// AllowReadOnly lets acmd itself set a ReadOnly setting, such as the
// editor's launcher once it's found.
const AllowReadOnly SetOption = 1

// Set changes settings by key, all or none: each value is checked (Spec.Check)
// and written into the file as it is on disk now, keeping everything else.
// Nothing is written when nothing changes. It returns the settings in effect.
func (f *File) Set(changes map[string]any, opts ...SetOption) (Config, error) {
	readOnlyOK := slices.Contains(opts, AllowReadOnly)
	type change struct {
		spec  Spec
		value any
	}
	var list []change
	var errs []error
	for key, v := range changes {
		spec, ok := Find(key)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%w %q", ErrUnknownSetting, key))
			continue
		case spec.ReadOnly && !readOnlyOK:
			errs = append(errs, fmt.Errorf("%s is set by acmd, not by hand", spec.Key))
			continue
		}
		value, err := spec.Check(v)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		list = append(list, change{spec, value})
	}
	if err := errors.Join(errs...); err != nil {
		return f.Config(), err
	}
	// In the order the settings are listed, so the file reads the same
	// whichever order they came in.
	slices.SortFunc(list, func(a, b change) int {
		return slices.IndexFunc(Specs, func(s Spec) bool { return s.Key == a.spec.Key }) -
			slices.IndexFunc(Specs, func(s Spec) bool { return s.Key == b.spec.Key })
	})

	f.mu.Lock()
	var err error
	for range 5 { // a hand edit while it writes: start again from it
		f.loadLocked()
		if f.problem != nil {
			err = fmt.Errorf("%w (%s)", ErrBadFile, f.problem.Error())
			break
		}
		read := f.stamp
		text := read.text
		if !read.exists {
			text = Template
		}
		if text, err = Retire(text); err != nil {
			break
		}
		for _, c := range list {
			if text, err = Set(text, c.spec.Section(), c.spec.Name(), c.value); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		if read.exists && text == read.text {
			break // nothing changed
		}
		if f.beforeWrite != nil {
			f.beforeWrite()
		}
		if now := f.readLocked(); !now.same(read) || now.text != read.text {
			err = errChangedOnDisk
			continue
		}
		err = f.writeLocked(text)
		break
	}
	if errors.Is(err, errChangedOnDisk) {
		err = errors.New("config.toml kept changing while it was being saved: try again")
	}
	if err != nil {
		cur := f.cur
		f.mu.Unlock()
		return cur, err
	}
	err = f.tellAfterWrite()
	return f.Config(), err
}

var errChangedOnDisk = errors.New("changed on disk")

// tellAfterWrite tells the OnChange funcs about what was just written
// (f.mu is held, and released here).
func (f *File) tellAfterWrite() error {
	f.mu.Unlock()
	f.Reload()
	return nil
}

// Edit changes the file's text with fn, as it is on disk now, and checks the
// result parses. It's for acmd's own one-off changes, such as moving data
// out of it.
func (f *File) Edit(fn func(text string) (string, error)) error {
	f.mu.Lock()
	f.loadLocked()
	if f.problem != nil {
		f.mu.Unlock()
		return fmt.Errorf("%w (%s)", ErrBadFile, f.problem.Error())
	}
	text, err := fn(f.stamp.text)
	if err == nil && text != f.stamp.text {
		if _, err = Parse([]byte(text)); err == nil {
			err = f.writeLocked(text)
		}
	}
	if err != nil {
		f.mu.Unlock()
		return err
	}
	return f.tellAfterWrite()
}

// Text is the file as it is on disk ("" when it's missing).
func (f *File) Text() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readLocked().text
}

// loadLocked reads the file: a good one is now in effect; a bad one keeps
// what was (f.mu is held).
func (f *File) loadLocked() {
	f.stamp = f.readLocked()
	if !f.stamp.exists {
		f.cur, f.problem = Default(), nil
		return
	}
	c, err := Parse([]byte(f.stamp.text))
	if err != nil {
		f.problem = problemOf(err)
		return
	}
	f.cur, f.problem = c, nil
}

func (f *File) statLocked() stamp {
	if f.path == "" {
		return stamp{}
	}
	info, err := os.Stat(f.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return stamp{exists: true, text: "", mod: time.Now()}
		}
		return stamp{}
	}
	return stamp{mod: info.ModTime(), size: info.Size(), exists: true}
}

// readLocked is statLocked, with the file's text (f.mu is held).
func (f *File) readLocked() stamp {
	st := f.statLocked()
	if !st.exists || f.path == "" {
		return st
	}
	b, err := os.ReadFile(f.path) // #nosec G304 -- acmd's own config file
	if err == nil {
		st.text = string(b)
	}
	return st
}

// writeLocked replaces the file with text (f.mu is held).
func (f *File) writeLocked(text string) error {
	if f.path == "" {
		return errors.New("there's no config.toml to change")
	}
	if err := writeAtomic(f.path, []byte(text)); err != nil {
		return err
	}
	f.stamp = f.readLocked()
	return nil
}

func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if target, err := filepath.EvalSymlinks(path); err == nil {
		if st, err := os.Stat(target); err == nil {
			mode = st.Mode().Perm()
		}
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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

// Backup copies config.toml next to itself as config.toml.<suffix>, and says
// where. A missing file has nothing to back up ("").
func (f *File) Backup(suffix string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.readLocked()
	if !st.exists {
		return "", nil
	}
	dst := f.path + "." + suffix
	if err := writeAtomic(dst, []byte(st.text)); err != nil {
		return "", err
	}
	return dst, nil
}

func problemOf(err error) *Problem {
	var pe toml.ParseError
	if errors.As(err, &pe) {
		msg := pe.Message
		if msg == "" {
			msg = strings.TrimPrefix(err.Error(), "config.toml: ")
		}
		return &Problem{Line: pe.Position.Line, Message: msg}
	}
	return &Problem{Message: strings.TrimPrefix(err.Error(), "config.toml: ")}
}

// equalConfig compares two configs by what they say.
func equalConfig(a, b Config) bool {
	ea, eb := new(bytes.Buffer), new(bytes.Buffer)
	_ = toml.NewEncoder(ea).Encode(a)
	_ = toml.NewEncoder(eb).Encode(b)
	return ea.String() == eb.String()
}
