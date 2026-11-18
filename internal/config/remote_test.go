package config

import (
	"crypto/sha256"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

var testFingerprint = FingerprintPrefix + base64.RawStdEncoding.EncodeToString(func() []byte {
	s := sha256.Sum256([]byte("a cert"))
	return s[:]
}())

func TestRemoteDefaults(t *testing.T) {
	r := Default().Remote
	want := Remote{Host: RemoteHost{Listen: "localhost", Port: 7433, PortFallback: true}}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("defaults: %+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("defaults don't validate: %v", err)
	}
	if got := r.Host.BindAddress(); got != "127.0.0.1:7433" {
		t.Errorf("bind: %s", got)
	}
}

func TestParseRemote(t *testing.T) {
	text := `[remote]
new_sessions_remote = true

[remote.host]
enabled = true
listen = "all"
port = 7500

[remote.client]
cert = "/c.pem"
key = "/c.key"

[[remote.authorized_clients]]
name = "laptop"
fingerprint = "` + testFingerprint + `"

[[remote.hosts]]
name = "studio-mac"
address = "192.168.1.20:7433"
fingerprint = "` + testFingerprint + `"

[[remote.hosts]]
name = "tunnel"
address = "localhost:17433"
cert = "/host.pem"
`
	c, err := Parse([]byte(text))
	must(t, err)
	want := Remote{
		NewSessionsRemote: true,
		// port_fallback isn't in the file: its default stays.
		Host:              RemoteHost{Enabled: true, Listen: "all", Port: 7500, PortFallback: true},
		Client:            RemoteClient{Cert: "/c.pem", Key: "/c.key"},
		AuthorizedClients: []AuthorizedClient{{Name: "laptop", Fingerprint: testFingerprint}},
		Hosts: []KnownHost{
			{Name: "studio-mac", Address: "192.168.1.20:7433", Fingerprint: testFingerprint},
			{Name: "tunnel", Address: "localhost:17433", Cert: "/host.pem"},
		},
	}
	if !reflect.DeepEqual(c.Remote, want) {
		t.Errorf("got %+v\nwant %+v", c.Remote, want)
	}
	if err := c.Remote.Validate(); err != nil {
		t.Error(err)
	}
	if got := c.Remote.Host.BindAddress(); got != "0.0.0.0:7500" {
		t.Errorf("bind: %s", got)
	}
}

func TestBindAddress(t *testing.T) {
	for _, tc := range []struct {
		host RemoteHost
		want string
	}{
		{RemoteHost{Listen: ListenLocalhost, Port: 7433}, "127.0.0.1:7433"},
		{RemoteHost{Listen: ListenAll, Port: 1}, "0.0.0.0:1"},
		{RemoteHost{Listen: "", Port: 65535}, "127.0.0.1:65535"},
		{RemoteHost{Listen: "0.0.0.0", Port: 80}, "127.0.0.1:80"}, // not a choice: localhost
	} {
		if got := tc.host.BindAddress(); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.host, got, tc.want)
		}
	}
}

func TestValidFingerprint(t *testing.T) {
	for _, tc := range []struct {
		fp   string
		want bool
	}{
		{testFingerprint, true},
		{"SHA256:" + strings.Repeat("A", 43), true},
		{"", false},
		{strings.TrimPrefix(testFingerprint, "SHA256:"), false},
		{"sha256:" + testFingerprint[7:], false},
		{"SHA256:" + strings.Repeat("A", 42), false},
		{"SHA256:" + strings.Repeat("A", 44), false},
		{"SHA256:" + strings.Repeat("A", 42) + "=", false},
		{"SHA256:" + strings.Repeat("A", 42) + "!", false},
		{"SHA256:" + strings.Repeat("A", 42) + "B", false}, // trailing bits set
		{"SHA256:" + strings.Repeat("-", 43), false},       // URL alphabet
	} {
		if got := ValidFingerprint(tc.fp); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.fp, got, tc.want)
		}
	}
}

func TestRemoteValidate(t *testing.T) {
	ok := func(*Remote) {}
	for _, tc := range []struct {
		name string
		edit func(*Remote)
		want []string // in the error; none for valid
	}{
		{"defaults", ok, nil},
		{"all", func(r *Remote) { r.Host.Listen = ListenAll }, nil},
		{"listen", func(r *Remote) { r.Host.Listen = "0.0.0.0" }, []string{`host.listen is "localhost" or "all", not "0.0.0.0"`}},
		{"port 0", func(r *Remote) { r.Host.Port = 0 }, []string{"host.port is from 1 to 65535, not 0"}},
		{"port high", func(r *Remote) { r.Host.Port = 65536 }, []string{"not 65536"}},
		{"port max", func(r *Remote) { r.Host.Port = 65535 }, nil},
		{"client", func(r *Remote) {
			r.AuthorizedClients = []AuthorizedClient{{Name: "a", Fingerprint: testFingerprint}, {Fingerprint: testFingerprint}}
		}, nil},
		{"client fingerprint", func(r *Remote) {
			r.AuthorizedClients = []AuthorizedClient{{Name: "a", Fingerprint: "SHA256:nope"}}
		}, []string{`authorized_clients[0] ("a"): fingerprint "SHA256:nope"`}},
		{"client no fingerprint", func(r *Remote) { r.AuthorizedClients = []AuthorizedClient{{Name: "a"}} },
			[]string{`authorized_clients[0] ("a"): fingerprint ""`}},
		{"host by cert", func(r *Remote) { r.Hosts = []KnownHost{{Name: "h", Address: "h:1", Cert: "/c"}} }, nil},
		{"host by both", func(r *Remote) {
			r.Hosts = []KnownHost{{Name: "h", Address: "[::1]:7433", Cert: "/c", Fingerprint: testFingerprint}}
		}, nil},
		{"host neither", func(r *Remote) { r.Hosts = []KnownHost{{Name: "h", Address: "h:1"}} },
			[]string{`hosts[0] ("h"): needs a fingerprint or a cert`}},
		{"host fingerprint", func(r *Remote) { r.Hosts = []KnownHost{{Name: "h", Address: "h:1", Fingerprint: "x"}} },
			[]string{`hosts[0] ("h"): fingerprint "x"`}},
		{"host no name", func(r *Remote) { r.Hosts = []KnownHost{{Name: " ", Address: "h:1", Cert: "/c"}} },
			[]string{"hosts[0]: name can't be empty"}},
		{"host twice", func(r *Remote) {
			r.Hosts = []KnownHost{{Name: "h", Address: "h:1", Cert: "/c"}, {Name: "h", Address: "h:2", Cert: "/c"}}
		}, []string{`hosts[1]: there's already a host named "h"`}},
		{"host address", func(r *Remote) {
			r.Hosts = []KnownHost{{Name: "a", Address: "h", Cert: "/c"}, {Name: "b", Address: "h:0", Cert: "/c"}, {Name: "c", Address: "h:x", Cert: "/c"}}
		}, []string{`hosts[0] ("a"): address is host:port, not "h"`, `"h:0"`, `"h:x"`}},
		{"several", func(r *Remote) { r.Host.Port = -1; r.Host.Listen = "" }, []string{"host.listen", "host.port"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := DefaultRemote()
			tc.edit(&r)
			err := r.Validate()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q isn't in:\n%v", w, err)
				}
			}
		})
	}
}
