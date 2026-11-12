package main

import (
	"os"
	"strings"
	"testing"
)

const sample = `{"data":[{"files":[
 {"filename":"/r/app/Sources/AgentCommand/Jobs.swift","summary":{"lines":{"percent":91.25}}},
 {"filename":"/r/app/Sources/AgentCommand/Links.swift","summary":{"lines":{"percent":40}}},
 {"filename":"/r/app/Tests/AgentCommandTests/Jobs.swift","summary":{"lines":{"percent":0}}}
]}]}`

func TestGate(t *testing.T) {
	var out strings.Builder
	ok, err := gate(strings.NewReader(sample), &out, map[string]float64{"Jobs.swift": 90})
	if err != nil || !ok || out.String() != "ok   Jobs.swift: 91.2% (minimum 90%)\n" {
		t.Errorf("pass: %v %v %q", ok, err, out.String())
	}

	out.Reset()
	ok, err = gate(strings.NewReader(sample), &out, map[string]float64{"Links.swift": 95, "Gone.swift": 10})
	want := "FAIL Gone.swift: not in the coverage report\nFAIL Links.swift: 40.0% (minimum 95%)\n"
	if err != nil || ok || out.String() != want {
		t.Errorf("fail: %v %v %q", ok, err, out.String())
	}

	for _, bad := range []string{"not json", `{"data":[]}`} {
		if _, err := gate(strings.NewReader(bad), &out, minimums); err == nil {
			t.Errorf("%q read", bad)
		}
	}
}

func TestEveryMinimumIsAPercent(t *testing.T) {
	for name, m := range minimums {
		if !strings.HasSuffix(name, ".swift") || m <= 0 || m > 100 {
			t.Errorf("%s: %v", name, m)
		}
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	report := dir + "/codecov.json"
	if err := os.WriteFile(report, []byte(`{"data":[{"files":[]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want int
	}{{nil, 2}, {[]string{dir + "/none.json"}, 2}, {[]string{report}, 1}} {
		if got := check(tc.args); got != tc.want {
			t.Errorf("%v: %d", tc.args, got)
		}
	}
	bad := dir + "/bad.json"
	if err := os.WriteFile(bad, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := check([]string{bad}); got != 2 {
		t.Errorf("bad: %d", got)
	}
}
