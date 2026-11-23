package cli

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/version"
)

// twoBuilds are two files standing in for the binary before and after an
// upgrade, and a watch whose stat says which one the path is now.
func twoBuilds(t *testing.T) (w *binaryWatch, now *atomic.Value, before, after string) {
	t.Helper()
	dir := t.TempDir()
	before, after = filepath.Join(dir, "0.90.0"), filepath.Join(dir, "0.91.0")
	for _, f := range []string{before, after} {
		must(t, os.WriteFile(f, []byte(f), 0o755))
	}
	now = &atomic.Value{}
	now.Store(before)
	cur := func() string { return now.Load().(string) }
	st, err := os.Stat(before)
	must(t, err)
	versions := map[string]string{before: "0.90.0", after: "0.91.0"}
	w = &binaryWatch{path: "/opt/homebrew/bin/acmd", start: st, running: "0.90.0",
		stat: func(string) (os.FileInfo, error) {
			file := cur() // whatever path is asked for, it's the build linked now
			return os.Stat(file)
		},
		versionOf: func(context.Context, string) (string, error) {
			if v, ok := versions[cur()]; ok && v != "" {
				return v, nil
			}
			return "", errors.New("exec format error")
		}}
	return w, now, before, after
}

func TestBinaryWatchSeesAnUpgrade(t *testing.T) {
	ctx := context.Background()
	w, now, _, after := twoBuilds(t)
	if v, ok := w.upgraded(ctx); ok {
		t.Errorf("the same binary is an upgrade to %s", v)
	}
	now.Store(after)
	if v, ok := w.upgraded(ctx); !ok || v != "0.91.0" {
		t.Errorf("after the upgrade: %q %v", v, ok)
	}
}

func TestBinaryWatchIgnoresWhatIsntANewVersion(t *testing.T) {
	ctx := context.Background()
	w, now, _, after := twoBuilds(t)
	now.Store(after)
	// Half written, or not acmd: not yet.
	w.versionOf = func(context.Context, string) (string, error) { return "", errors.New("exec format error") }
	if _, ok := w.upgraded(ctx); ok {
		t.Error("a binary that doesn't run is an upgrade")
	}
	// The same version reinstalled: the daemon stays, and it's the one now.
	w.versionOf = func(context.Context, string) (string, error) { return "0.90.0", nil }
	if _, ok := w.upgraded(ctx); ok {
		t.Error("the same version is an upgrade")
	}
	w.versionOf = func(context.Context, string) (string, error) { t.Error("looked at it again"); return "", nil }
	if _, ok := w.upgraded(ctx); ok {
		t.Error("the reinstalled binary is an upgrade")
	}
	// Gone (brew between unlink and link): not yet.
	w.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if _, ok := w.upgraded(ctx); ok {
		t.Error("a missing binary is an upgrade")
	}
}

func TestFollowCallsOnceOnTheUpgrade(t *testing.T) {
	w, now, _, after := twoBuilds(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 2)
	done := make(chan struct{})
	go func() { w.follow(ctx, time.Millisecond, func(v string) { got <- v }); close(done) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case v := <-got:
		t.Fatalf("an upgrade to %s before there was one", v)
	default:
	}
	now.Store(after)
	select {
	case v := <-got:
		if v != "0.91.0" {
			t.Errorf("upgraded to %s", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never saw the upgrade")
	}
	<-done // it stops once it's said so
}

func TestBinaryVersionReadsAcmdsVersionLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	dir := t.TempDir()
	acmd, other := filepath.Join(dir, "acmd"), filepath.Join(dir, "other")
	must(t, os.WriteFile(acmd, []byte("#!/bin/sh\necho 'agent-command 0.91.0 (commit abc, exact screen)'\n"), 0o755))
	must(t, os.WriteFile(other, []byte("#!/bin/sh\necho 'something else'\n"), 0o755))
	if v, err := binaryVersion(context.Background(), acmd); err != nil || v != "0.91.0" {
		t.Errorf("acmd: %q %v", v, err)
	}
	if _, err := binaryVersion(context.Background(), other); err == nil {
		t.Error("read a version from something that isn't acmd")
	}
	if _, err := binaryVersion(context.Background(), filepath.Join(dir, "none")); err == nil {
		t.Error("read a version from nothing")
	}
	if w, err := newBinaryWatch(acmd, "0.90.0"); err != nil || w.start == nil {
		t.Errorf("newBinaryWatch: %v", err)
	}
	if _, err := newBinaryWatch(filepath.Join(dir, "none"), "0.90.0"); err == nil {
		t.Error("watched a binary that isn't there")
	}
}

// The service's daemon ends itself once the binary its service runs is
// another version: the service then starts that one.
func TestTheServicesDaemonMakesWayForAnUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	f := withService(t, "darwin")
	bin := filepath.Join(dir, "acmd")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\necho 'agent-command "+version.Get().Version+" (commit a)'\n"), 0o755))
	_, err := f.Env().Install(context.Background(), bin, service.InstallOptions{NoStart: true})
	must(t, err)
	orig := upgradeEvery
	upgradeEvery = 5 * time.Millisecond
	t.Cleanup(func() { upgradeEvery = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan struct{})
	var logs syncBuffer
	followUpgrades(ctx, func() { close(ended) }, log.New(&logs, "", 0))
	time.Sleep(30 * time.Millisecond)
	select {
	case <-ended:
		t.Fatal("ended with no upgrade")
	default:
	}
	// brew links the new version: a new file at the same path.
	newer := filepath.Join(dir, "acmd.new")
	must(t, os.WriteFile(newer, []byte("#!/bin/sh\necho 'agent-command 99.0.0 (commit b)'\n"), 0o755))
	must(t, os.Rename(newer, bin))
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't make way")
	}
	if !strings.Contains(logs.String(), "is acmd 99.0.0 now") {
		t.Errorf("logged %q", logs.String())
	}
}
