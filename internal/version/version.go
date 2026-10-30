// Package version reports build information for agent-command.
//
// Version, Commit and Date are set at build time via -ldflags (see Makefile
// and .goreleaser.yaml). When they are not set, Get falls back to the Go
// toolchain's embedded VCS information.
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// DevVersion is the version reported by builds without release ldflags.
const DevVersion = "0.0.0-dev"

// Set via -ldflags "-X github.com/brettmostert/agent-command/internal/version.<Name>=<value>".
var (
	Version = DevVersion
	Commit  = ""
	Date    = ""
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns build information for the running binary.
func Get() Info {
	return resolve(Version, Commit, Date, readBuildInfo)
}

func readBuildInfo() (*debug.BuildInfo, bool) { return debug.ReadBuildInfo() }

func resolve(v, commit, date string, buildInfo func() (*debug.BuildInfo, bool)) Info {
	info := Info{
		Version:   strings.TrimPrefix(v, "v"),
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info.Version != DevVersion {
		return info
	}

	bi, ok := buildInfo()
	if !ok {
		return info
	}
	// `go install module@vX.Y.Z` records the module version.
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		info.Version = strings.TrimPrefix(mv, "v")
	}
	var revision string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if info.Commit == "" {
		info.Commit = revision
	}
	if info.Version == DevVersion && revision != "" {
		info.Version += "+" + short(revision)
		if modified {
			info.Version += ".dirty"
		}
	}
	return info
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// String renders a single human-readable line, e.g.
// "agent-command 0.1.0 (commit 1a2b3c4, built 2026-10-30T12:00:00Z, go1.27.2 darwin/arm64)".
func (i Info) String() string {
	parts := make([]string, 0, 3)
	if i.Commit != "" {
		parts = append(parts, "commit "+short(i.Commit))
	}
	if i.Date != "" {
		parts = append(parts, "built "+i.Date)
	}
	parts = append(parts, i.GoVersion+" "+i.Platform)
	return fmt.Sprintf("agent-command %s (%s)", i.Version, strings.Join(parts, ", "))
}

// JSON renders the info as indented JSON.
func (i Info) JSON() ([]byte, error) {
	return json.MarshalIndent(i, "", "  ")
}
