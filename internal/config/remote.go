package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Remote is the [remote] section: Remote sessions, used from another acmd.
// New sessions read NewSessionsRemote, and doctor and `acmd remote sessions`
// read Host.Enabled; Hosting and Remote hosts build on the rest.
type Remote struct {
	// NewSessionsRemote is ⌘N's default for a new session's Remote checkbox.
	NewSessionsRemote bool               `toml:"new_sessions_remote"`
	Host              RemoteHost         `toml:"host"`
	Client            RemoteClient       `toml:"client"`
	AuthorizedClients []AuthorizedClient `toml:"authorized_clients"` // on a host
	Hosts             []KnownHost        `toml:"hosts"`              // on a client
}

// RemoteHost is [remote.host]: Hosting, this acmd's listener for clients.
type RemoteHost struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"` // ListenLocalhost or ListenAll
	Port    int    `toml:"port"`
	// PortFallback picks a free port when Port is taken, instead of failing.
	PortFallback bool   `toml:"port_fallback"`
	Cert         string `toml:"cert"` // "" is acmd's generated identity
	Key          string `toml:"key"`
}

// RemoteClient is [remote.client]: the cert this acmd shows the hosts it
// connects to.
type RemoteClient struct {
	Cert string `toml:"cert"` // "" is acmd's generated identity
	Key  string `toml:"key"`
}

// AuthorizedClient is one [[remote.authorized_clients]]: a client this host lets in.
type AuthorizedClient struct {
	Name        string `toml:"name"`
	Fingerprint string `toml:"fingerprint"`
}

// KnownHost is one [[remote.hosts]]: a Remote host this client can connect to,
// pinned by its cert's fingerprint or by the cert itself.
type KnownHost struct {
	Name        string `toml:"name"`
	Address     string `toml:"address"` // host:port
	Fingerprint string `toml:"fingerprint"`
	Cert        string `toml:"cert"` // a path to the host's cert, in place of Fingerprint
}

// Where Hosting listens.
const (
	ListenLocalhost = "localhost" // 127.0.0.1
	ListenAll       = "all"       // 0.0.0.0
)

// DefaultRemotePort is Hosting's port unless the user picks another.
const DefaultRemotePort = 7433

// DefaultRemote is [remote] with nothing set: Hosting off, on localhost:7433,
// falling back to another port when that one is taken.
func DefaultRemote() Remote {
	return Remote{Host: RemoteHost{Listen: ListenLocalhost, Port: DefaultRemotePort, PortFallback: true}}
}

// BindAddress is where Hosting listens: "127.0.0.1:port", or "0.0.0.0:port"
// for ListenAll. Anything else is localhost, the safe choice.
func (h RemoteHost) BindAddress() string {
	ip := "127.0.0.1"
	if h.Listen == ListenAll {
		ip = "0.0.0.0"
	}
	return net.JoinHostPort(ip, strconv.Itoa(h.Port))
}

// FingerprintPrefix starts every fingerprint: "SHA256:" and then the cert's
// SHA-256 in unpadded base64 (43 characters).
const FingerprintPrefix = "SHA256:"

// ValidFingerprint reports whether s is a fingerprint: "SHA256:" and 43
// characters of unpadded base64 that decode to 32 bytes.
func ValidFingerprint(s string) bool {
	b64, ok := strings.CutPrefix(s, FingerprintPrefix)
	if !ok || len(b64) != 43 {
		return false
	}
	sum, err := base64.RawStdEncoding.Strict().DecodeString(b64)
	return err == nil && len(sum) == 32
}

// Validate checks [remote], and returns every problem it finds, or nil.
func (r Remote) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf("remote."+format, a...)) }

	if l := r.Host.Listen; l != ListenLocalhost && l != ListenAll {
		bad("host.listen is %q or %q, not %q", ListenLocalhost, ListenAll, l)
	}
	if p := r.Host.Port; p < 1 || p > 65535 {
		bad("host.port is from 1 to 65535, not %d", p)
	}
	for i, c := range r.AuthorizedClients {
		if !ValidFingerprint(c.Fingerprint) {
			bad("authorized_clients[%d] (%q): fingerprint %q isn't SHA256: and 43 base64 characters", i, c.Name, c.Fingerprint)
		}
	}
	names := map[string]bool{}
	for i, h := range r.Hosts {
		switch {
		case strings.TrimSpace(h.Name) == "":
			bad("hosts[%d]: name can't be empty", i)
		case names[h.Name]:
			bad("hosts[%d]: there's already a host named %q", i, h.Name)
		}
		names[h.Name] = true
		if _, port, err := net.SplitHostPort(h.Address); err != nil || !validPort(port) {
			bad("hosts[%d] (%q): address is host:port, not %q", i, h.Name, h.Address)
		}
		if h.Fingerprint == "" && h.Cert == "" {
			bad("hosts[%d] (%q): needs a fingerprint or a cert", i, h.Name)
		}
		if h.Fingerprint != "" && !ValidFingerprint(h.Fingerprint) {
			bad("hosts[%d] (%q): fingerprint %q isn't SHA256: and 43 base64 characters", i, h.Name, h.Fingerprint)
		}
	}
	return errors.Join(errs...)
}

func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}
