// Command app-coverage enforces line coverage for the app's logic, file by file.
//
//	go run ./scripts/app-coverage <codecov.json>
//
// Only the files below. Views and window handling need a running UI, so a
// target for the whole app would only encourage low-value tests of them. Raise
// a minimum when a file's coverage rises, and add a file when its logic gets
// tests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
)

var minimums = map[string]float64{
	"AppNotifier.swift":     30,
	"Daemon.swift":          85,
	"InboxModel.swift":      90,
	"Jobs.swift":            90,
	"Links.swift":           95,
	"Markdown.swift":        90,
	"MarkdownEditing.swift": 95,
	"Palette.swift":         95,
	"PaneSets.swift":        95,
	"PaneTree.swift":        95,
	// The rest reads the person's real config, which tests don't.
	"RemoteControl.swift": 60,
	"Report.swift":        95,
	// The rest opens panels and runs timers.
	"ScheduledModel.swift":   80,
	"ScheduleBuilder.swift":  95,
	"SessionStore.swift":     70,
	"SpeechController.swift": 90,
	"StatusSummary.swift":    95,
	"ThemeConfig.swift":      95,
}

// report is the part of `swift test --show-codecov-path`'s JSON we read.
type report struct {
	Data []struct {
		Files []struct {
			Filename string `json:"filename"`
			Summary  struct {
				Lines struct {
					Percent float64 `json:"percent"`
				} `json:"lines"`
			} `json:"summary"`
		} `json:"files"`
	} `json:"data"`
}

func main() { os.Exit(check(os.Args[1:])) }

// check gates the report named in args: 0 passed, 1 failed, 2 couldn't read it.
func check(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: app-coverage <codecov.json>")
		return 2
	}
	f, err := os.Open(args[0]) //nolint:gosec // G703: the report the person names on the command line
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer f.Close()
	ok, err := gate(f, os.Stdout, minimums)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 2
	case !ok:
		return 1
	}
	return 0
}

// gate prints each file's coverage against its minimum, and says whether all
// of them met it.
func gate(in io.Reader, out io.Writer, mins map[string]float64) (bool, error) {
	var r report
	if err := json.NewDecoder(in).Decode(&r); err != nil {
		return false, fmt.Errorf("read the coverage report: %w", err)
	}
	if len(r.Data) == 0 {
		return false, fmt.Errorf("the coverage report has no data")
	}
	found := map[string]float64{}
	for _, f := range r.Data[0].Files {
		if strings.Contains(f.Filename, "/Sources/AgentCommand/") {
			found[path.Base(f.Filename)] = f.Summary.Lines.Percent
		}
	}
	names := make([]string, 0, len(mins))
	for name := range mins {
		names = append(names, name)
	}
	slices.Sort(names)
	passed := true
	for _, name := range names {
		got, ok := found[name]
		if !ok {
			fmt.Fprintf(out, "FAIL %s: not in the coverage report\n", name)
			passed = false
			continue
		}
		status := "ok  "
		if got < mins[name] {
			status, passed = "FAIL", false
		}
		fmt.Fprintf(out, "%s %s: %.1f%% (minimum %g%%)\n", status, name, got, mins[name])
	}
	return passed, nil
}
