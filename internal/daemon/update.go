package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// Updates: the daemon looks for newer releases of acmd itself, on the
// Homebrew tap where they're published, unless updates.check is off. A
// headless host has nobody else to look for it. With the setting off it
// makes no request at all.

const (
	defaultReleasesURL = "https://api.github.com/repos/brettmostert/homebrew-tap/releases?per_page=30"
	releaseTagPrefix   = "agent-command-v"
	releasePageURL     = "https://github.com/brettmostert/homebrew-tap/releases/tag/"
)

var updatesEvery = 6 * time.Hour

type updateState struct {
	mu     sync.Mutex
	latest string // newer than this one; "" when there's none, or it isn't known
}

func (d *Daemon) startUpdates(ctx context.Context) {
	if parseVersion(d.cfg.Version) == nil {
		return // a development build has nothing to compare
	}
	go func() {
		t := time.NewTicker(updatesEvery)
		defer t.Stop()
		for {
			d.checkUpdates(ctx)
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-t.C:
			}
		}
	}()
}

// checkUpdates asks for the releases, when updates.check is on.
func (d *Daemon) checkUpdates(ctx context.Context) {
	if !d.config().Updates.Check {
		d.setLatest("")
		return
	}
	url := d.cfg.ReleasesURL
	if url == "" {
		url = defaultReleasesURL
	}
	client := d.cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		d.cfg.Log.Printf("updates: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		d.cfg.Log.Printf("updates: the releases said %s", resp.Status)
		return
	}
	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		d.cfg.Log.Printf("updates: %v", err)
		return
	}
	d.setLatest(newerRelease(d.cfg.Version, releases))
}

func (d *Daemon) setLatest(v string) {
	d.updates.mu.Lock()
	changed := d.updates.latest != v
	d.updates.latest = v
	d.updates.mu.Unlock()
	if changed {
		d.changes.bump()
	}
}

// update is this daemon's version, and the newer release if there is one.
func (d *Daemon) update() *ipc.Update {
	d.updates.mu.Lock()
	defer d.updates.mu.Unlock()
	u := &ipc.Update{Current: d.cfg.Version}
	if d.updates.latest != "" {
		u.Latest, u.URL = d.updates.latest, releasePageURL+releaseTagPrefix+d.updates.latest
	}
	return u
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// newerRelease is the newest of acmd's published releases, when it's newer
// than current ("" otherwise). The tap has other releases too (the speech
// model): only agent-command-vX.Y.Z count.
func newerRelease(current string, releases []release) string {
	mine := parseVersion(current)
	if mine == nil {
		return ""
	}
	best, bestV := "", mine
	for _, r := range releases {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, releaseTagPrefix) {
			continue
		}
		v := strings.TrimPrefix(r.Tag, releaseTagPrefix)
		if p := parseVersion(v); p != nil && less(bestV, p) {
			best, bestV = v, p
		}
	}
	return best
}

// parseVersion reads "0.64.0" or "v0.64.0". Anything else, such as a
// development build's "0.64.0+dirty", is nil.
func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func less(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
