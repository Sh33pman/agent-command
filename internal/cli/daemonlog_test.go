package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLog(t *testing.T, path, text string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestRotateLogStartsANewFileEachDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	if moved, err := rotateLog(path, now); moved || err != nil {
		t.Errorf("no log: moved %v, %v", moved, err)
	}
	writeLog(t, path, "today", now.Add(-time.Hour))
	if moved, err := rotateLog(path, now); moved || err != nil {
		t.Errorf("today's log: moved %v, %v", moved, err)
	}
	writeLog(t, path, "yesterday", now.AddDate(0, 0, -1))
	if moved, err := rotateLog(path, now); !moved || err != nil {
		t.Errorf("yesterday's log: moved %v, %v", moved, err)
	}
	if b, err := os.ReadFile(path + ".2026-11-06"); err != nil || string(b) != "yesterday" {
		t.Errorf("dated log: %q, %v", b, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the log wasn't moved")
	}
}

func TestRotateLogKeepsThreeDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	kept := []string{".2026-11-06", ".2026-11-05", ".notes"}
	for _, suffix := range append([]string{".2026-11-04", ".2026-10-01"}, kept...) {
		writeLog(t, path+suffix, "x", now)
	}
	if _, err := rotateLog(path, now); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".2026-11-04", ".2026-10-01"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Errorf("%s is still there", suffix)
		}
	}
	for _, suffix := range kept {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Errorf("%s went: %v", suffix, err)
		}
	}
}

func TestRotateLogStopsOneDayFillingTheDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	writeLog(t, path, "small", now)
	if err := os.Truncate(path, daemonLogLimit); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	if moved, err := rotateLog(path, now); !moved || err != nil {
		t.Errorf("big log: moved %v, %v", moved, err)
	}
	if _, err := os.Stat(path + ".2026-11-07"); err != nil {
		t.Error(err)
	}
}
