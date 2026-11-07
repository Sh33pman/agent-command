package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"time"

	xterm "github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

const (
	// logDays is how many days of logs are kept, today's included.
	logDays = 3
	// daemonLogLimit moves a day's log aside early, so a daemon logging far too much
	// can't fill the disk. That day's log replaces what it set aside before.
	daemonLogLimit = 20 << 20
	// daemonLogCheck is how often a running daemon checks the log.
	daemonLogCheck = 10 * time.Minute
)

var datedLog = regexp.MustCompile(`\.(\d{4}-\d{2}-\d{2})$`)

// logDaemonOutput points this process's output at the log, however it was started:
// the app and others discard it or point it elsewhere. That covers log lines, start-up
// errors and crashes. Run from a terminal, the output stays there.
func logDaemonOutput(ctx context.Context, path string) error {
	if xterm.IsTerminal(os.Stderr.Fd()) {
		return nil
	}
	if _, err := rotateLog(path, time.Now()); err != nil {
		return err
	}
	if err := pointOutputAt(path); err != nil {
		return err
	}
	go func() {
		tick := time.NewTicker(daemonLogCheck)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				// Without a log to write to, the output keeps going where it was.
				if rotated, err := rotateLog(path, now); err == nil && rotated {
					_ = pointOutputAt(path)
				}
			}
		}
	}()
	return nil
}

// rotateLog gives a log from an earlier day, or one that has grown too big, the date
// of its last entry, then removes logs from before the days being kept. It reports
// whether it moved the log.
func rotateLog(path string, now time.Time) (bool, error) {
	moved := false
	info, err := os.Stat(path)
	switch {
	case err == nil:
		last := info.ModTime()
		if day(last) != day(now) || info.Size() >= daemonLogLimit {
			if err := os.Rename(path, path+"."+day(last)); err != nil {
				return false, err
			}
			moved = true
		}
	case !os.IsNotExist(err):
		return false, err
	}
	return moved, removeOldLogs(path, now)
}

func day(t time.Time) string { return t.Format("2006-01-02") }

// removeOldLogs removes dated logs older than the days being kept.
func removeOldLogs(path string, now time.Time) error {
	oldest := day(now.AddDate(0, 0, -(logDays - 1)))
	files, err := filepath.Glob(path + ".*")
	if err != nil {
		return err
	}
	for _, f := range files {
		m := datedLog.FindStringSubmatch(f)
		// Dates in this form sort as text.
		if m != nil && m[1] < oldest {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// pointOutputAt makes stdout and stderr append to path.
func pointOutputAt(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, fd := range []int{1, 2} {
		if err := unix.Dup2(int(f.Fd()), fd); err != nil {
			return err
		}
	}
	return nil
}
