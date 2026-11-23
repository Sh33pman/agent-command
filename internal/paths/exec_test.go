package paths

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	run, plain := filepath.Join(dir, "run"), filepath.Join(dir, "plain")
	for f, mode := range map[string]os.FileMode{run: 0o755, plain: 0o644} {
		if err := os.WriteFile(f, nil, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f, mode); err != nil {
			t.Fatal(err)
		}
	}
	stat := func(p string) fs.FileInfo {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if !IsExecutable(stat(run)) {
		t.Error("a file with an execute bit doesn't run")
	}
	if isExecutable("linux", stat(plain)) && stat(plain).Mode().Perm()&0o111 == 0 {
		t.Error("a file with no execute bit runs on Unix")
	}
	if !isExecutable("windows", stat(plain)) {
		t.Error("on Windows a regular file runs, by its extension")
	}
	for _, goos := range []string{"linux", "windows"} {
		if isExecutable(goos, stat(dir)) || isExecutable(goos, nil) {
			t.Errorf("%s: a folder, or nothing, runs", goos)
		}
	}
}
