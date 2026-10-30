package version

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func noBuildInfo() (*debug.BuildInfo, bool) { return nil, false }

func buildInfo(mainVersion string, settings map[string]string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}
		for k, v := range settings {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return bi, true
	}
}

func TestResolve(t *testing.T) {
	const sha = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"

	tests := []struct {
		name       string
		version    string
		commit     string
		date       string
		buildInfo  func() (*debug.BuildInfo, bool)
		wantVer    string
		wantCommit string
		wantDate   string
	}{
		{
			name:       "release ldflags win over build info",
			version:    "0.1.0",
			commit:     sha,
			date:       "2026-10-30T12:00:00Z",
			buildInfo:  buildInfo("v9.9.9", map[string]string{"vcs.revision": "ffff"}),
			wantVer:    "0.1.0",
			wantCommit: sha,
			wantDate:   "2026-10-30T12:00:00Z",
		},
		{
			name:     "leading v is stripped",
			version:  "v1.2.3",
			wantVer:  "1.2.3",
			wantDate: "",
		},
		{
			name:      "dev build without build info",
			version:   DevVersion,
			buildInfo: noBuildInfo,
			wantVer:   DevVersion,
		},
		{
			name:    "dev build from a clean checkout",
			version: DevVersion,
			buildInfo: buildInfo("(devel)", map[string]string{
				"vcs.revision": sha, "vcs.time": "2026-10-30T10:00:00Z", "vcs.modified": "false",
			}),
			wantVer:    DevVersion + "+1a2b3c4",
			wantCommit: sha,
			wantDate:   "2026-10-30T10:00:00Z",
		},
		{
			name:       "dev build from a dirty checkout",
			version:    DevVersion,
			buildInfo:  buildInfo("(devel)", map[string]string{"vcs.revision": sha, "vcs.modified": "true"}),
			wantVer:    DevVersion + "+1a2b3c4.dirty",
			wantCommit: sha,
		},
		{
			name:      "go install with a module version",
			version:   DevVersion,
			buildInfo: buildInfo("v0.4.2", nil),
			wantVer:   "0.4.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bi := tt.buildInfo
			if bi == nil {
				bi = noBuildInfo
			}
			got := resolve(tt.version, tt.commit, tt.date, bi)
			if got.Version != tt.wantVer {
				t.Errorf("Version = %q, want %q", got.Version, tt.wantVer)
			}
			if got.Commit != tt.wantCommit {
				t.Errorf("Commit = %q, want %q", got.Commit, tt.wantCommit)
			}
			if got.Date != tt.wantDate {
				t.Errorf("Date = %q, want %q", got.Date, tt.wantDate)
			}
			if got.GoVersion != runtime.Version() {
				t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
			}
			if want := runtime.GOOS + "/" + runtime.GOARCH; got.Platform != want {
				t.Errorf("Platform = %q, want %q", got.Platform, want)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	i := Info{Version: "0.1.0", Commit: "1a2b3c4d5e6f", Date: "2026-10-30T12:00:00Z", GoVersion: "go1.27.2", Platform: "darwin/arm64"}
	want := "agent-command 0.1.0 (commit 1a2b3c4, built 2026-10-30T12:00:00Z, go1.27.2 darwin/arm64)"
	if got := i.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	minimal := Info{Version: DevVersion, GoVersion: "go1.27.2", Platform: "darwin/arm64"}
	if got := minimal.String(); got != "agent-command 0.0.0-dev (go1.27.2 darwin/arm64)" {
		t.Errorf("String() without commit/date = %q", got)
	}
}

func TestInfoJSON(t *testing.T) {
	i := Info{Version: "0.1.0", Commit: "abc", Date: "d", GoVersion: "go1.27.2", Platform: "darwin/arm64"}
	b, err := i.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	for _, k := range []string{"version", "commit", "date", "go_version", "platform"} {
		if _, ok := got[k]; !ok {
			t.Errorf("JSON missing key %q in %s", k, b)
		}
	}
	if !strings.Contains(string(b), "\n") {
		t.Errorf("JSON should be indented, got %s", b)
	}
}
