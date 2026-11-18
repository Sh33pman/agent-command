package config

import (
	"strings"
	"testing"
)

const fpA = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const fpB = "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"

func TestAppendTable(t *testing.T) {
	client := func(name, fp string) []KeyValue {
		return []KeyValue{{"name", name}, {"fingerprint", fp}}
	}
	// The template's commented example stays, and the new table follows it.
	got, err := AppendTable(Template, "remote.authorized_clients", client("laptop", fpA)...)
	if err != nil {
		t.Fatal(err)
	}
	want := Template + "\n[[remote.authorized_clients]]\nname = \"laptop\"\nfingerprint = \"" + fpA + "\"\n"
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	got, err = AppendTable(got, "remote.authorized_clients", client("desk", fpB)...)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Remote.AuthorizedClients) != 2 || c.Remote.AuthorizedClients[1] != (AuthorizedClient{Name: "desk", Fingerprint: fpB}) {
		t.Errorf("clients = %+v", c.Remote.AuthorizedClients)
	}
	if !strings.HasPrefix(got, Template) {
		t.Error("the rest of the file changed")
	}

	for _, text := range []string{"", "x = 1"} {
		got, err := AppendTable(text, "a.b", KeyValue{"k", 1})
		if err != nil || !strings.HasSuffix(got, "[[a.b]]\nk = 1\n") || strings.HasPrefix(got, "\n") {
			t.Errorf("%q: %q %v", text, got, err)
		}
	}
	for name, text := range map[string]string{
		"bad toml":       "x = ",
		"not an array":   "[a]\nb = 1\n",
		"inline array":   "[a]\nb = [{k = 1}]\n",
		"a table before": "[a.b]\nk = 1\n",
	} {
		if _, err := AppendTable(text, "a.b", KeyValue{"k", 2}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := AppendTable("", "a", KeyValue{"k", nil}); err == nil {
		t.Error("a nil value")
	}
}

func TestRemoveTables(t *testing.T) {
	text := `# my config
x = 1

[[remote.authorized_clients]]  # the laptop
name = "laptop"
# its cert from the drawer
fingerprint = "` + fpA + `"

[[remote.authorized_clients]]
name = "desk"
fingerprint = "` + fpB + `"
# hosts follow

[[remote.hosts]]
name = "studio"
`
	byFP := func(fp string) func(map[string]any) bool {
		return func(m map[string]any) bool { return m["fingerprint"] == fp }
	}
	got, n, err := RemoveTables(text, "remote.authorized_clients", byFP(fpA))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	want := `# my config
x = 1

[[remote.authorized_clients]]
name = "desk"
fingerprint = "` + fpB + `"
# hosts follow

[[remote.hosts]]
name = "studio"
`
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	got, n, err = RemoveTables(got, "remote.authorized_clients", byFP(fpB))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	want = `# my config
x = 1
# hosts follow

[[remote.hosts]]
name = "studio"
`
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	// Nothing matches: the text comes back as it was.
	if got, n, err := RemoveTables(text, "remote.authorized_clients", byFP("x")); got != text || n != 0 || err != nil {
		t.Errorf("no match: %d %v", n, err)
	}
	// The last table in the file.
	got, _, err = RemoveTables("[[a]]\nk = 1\n[[a]]\nk = 2\n", "a", func(m map[string]any) bool { return m["k"] == int64(2) })
	if err != nil || got != "[[a]]\nk = 1\n" {
		t.Errorf("last: %q %v", got, err)
	}
	if _, _, err := RemoveTables("[[a]]\nk = \n", "a", byFP(fpA)); err == nil {
		t.Error("a table that doesn't parse")
	}
}
