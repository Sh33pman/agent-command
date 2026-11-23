package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustFind(t *testing.T, key string) Spec {
	t.Helper()
	s, ok := Find(key)
	if !ok {
		t.Fatalf("no setting %s", key)
	}
	return s
}

func TestEverySettingIsDescribed(t *testing.T) {
	seen := map[string]bool{}
	values := Values(Default())
	for _, s := range Specs {
		if seen[s.Key] {
			t.Errorf("%s twice", s.Key)
		}
		seen[s.Key] = true
		if s.Description == "" || (s.Where != WhereHost && s.Where != WhereDevice) || s.get == nil {
			t.Errorf("%s isn't fully described: %+v", s.Key, s)
		}
		// Its default is what Default() has.
		if !reflect.DeepEqual(values[s.Key], s.Default) {
			t.Errorf("%s: default %#v, but Default() has %#v", s.Key, s.Default, values[s.Key])
		}
		// Its default passes its own check.
		if !s.ReadOnly {
			if _, err := s.Check(s.Default); err != nil {
				t.Errorf("%s: its default fails: %v", s.Key, err)
			}
		}
	}
}

func TestDeviceAndCodeSettings(t *testing.T) {
	for _, s := range Specs {
		device := strings.HasPrefix(s.Key, "editor.") || strings.HasPrefix(s.Key, "speech.")
		if device != (s.Where == WhereDevice) {
			t.Errorf("%s is %s", s.Key, s.Where)
		}
	}
	var code []string
	for _, s := range Specs {
		if s.RunsCode {
			code = append(code, s.Key)
		}
	}
	if got := strings.Join(code, " "); got != "editor.command" {
		t.Fatalf("settings that run code: %s", got)
	}
}

func TestCheckTakesJSONAndTypedText(t *testing.T) {
	cases := []struct {
		key  string
		in   any
		want any
	}{
		{"notify.enabled", true, true},
		{"notify.enabled", "on", true},
		{"notify.enabled", "No", false},
		{"notify.cooldown", "90s", "1m30s"},
		{"remote.host.port", 8080.0, 8080},
		{"remote.host.port", "8080", 8080},
		{"remote.host.port", json.Number("9000"), 9000},
		{"speech.speed", 1.5, 1.5},
		{"speech.speed", "0.75", 0.75},
		{"editor.command", []any{"nvim", "{dir}"}, []string{"nvim", "{dir}"}},
		{"editor.command", `["a b", "c"]`, []string{"a b", "c"}},
		{"editor.command", "--x  --y", []string{"--x", "--y"}},
		{"editor.command", "", []string{}},
		{"editor.command", []string{"z"}, []string{"z"}},
		{"default_harness", "", ""},
		{"default_harness", " 01HARNESS ", "01HARNESS"},
		{"speech.voice", "af_bella:0.5,am_adam:0.5", "af_bella:0.5,am_adam:0.5"},
		{"remote_control", "true", true}, // its old name
	}
	for _, c := range cases {
		got, err := mustFind(t, c.key).Check(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %#v: got %#v, %v; want %#v", c.key, c.in, got, err, c.want)
		}
	}
}

func TestCheckRefusesWhatIsWrong(t *testing.T) {
	cases := []struct {
		key  string
		in   any
		want string
	}{
		{"notify.enabled", "maybe", "on or off"},
		{"notify.cooldown", "soon", "length of time"},
		{"notify.cooldown", "-5s", "length of time"},
		{"notify.cooldown", 5.0, "length of time"},
		{"remote.host.port", 0.0, "from 1 to 65535"},
		{"remote.host.port", 80.5, "whole number"},
		{"remote.host.port", "x", "whole number"},
		{"remote.host.port", true, "whole number"},
		{"speech.speed", 3.0, "from 0.5 to 2"},
		{"default_harness", 1.0, "is text"},
		{"editor.command", []any{"a", 1.0}, "list of text"},
		{"editor.command", "[nope", "editor.command"},
		{"editor.command", true, "is a list"},
		{"speech.voice", "bf_emma", "American English"},
	}
	for _, c := range cases {
		_, err := mustFind(t, c.key).Check(c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %#v: err %v, want %q", c.key, c.in, err, c.want)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Fatal("found a setting that isn't one")
	}
}

func TestSectionAndName(t *testing.T) {
	s := mustFind(t, "remote.host.port")
	if s.Section() != "remote.host" || s.Name() != "port" {
		t.Fatal(s.Section(), s.Name())
	}
	s = mustFind(t, "default_harness")
	if s.Section() != "" || s.Name() != "default_harness" {
		t.Fatal(s.Section(), s.Name())
	}
	if v := s.Value(Config{DefaultHarness: "x"}); !reflect.DeepEqual(v, "x") {
		t.Fatal(v)
	}
}
