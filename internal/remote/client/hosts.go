package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Known hosts are config.toml's [[remote.hosts]], read-only, and the ones
// added through ops, kept in Data. These change the second kind, and follow
// the change at once.

// Data is where the hosts added through ops are kept.
type Data interface {
	// KnownHosts are the hosts added, oldest first.
	KnownHosts() ([]config.KnownHost, error)
	// SaveKnownHost adds h, or replaces the host named replace with it.
	SaveKnownHost(replace string, h config.KnownHost) error
	// RemoveKnownHost forgets a host, and says whether it was there.
	RemoveKnownHost(name string) (bool, error)
}

// MemoryData is Data kept in memory, for tests and a Client with nowhere to
// keep it.
type MemoryData struct {
	mu    sync.Mutex
	Hosts []config.KnownHost
	// Fail, when set, fails every change, as a full disk would.
	Fail error
}

func (m *MemoryData) KnownHosts() ([]config.KnownHost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.Hosts), nil
}

func (m *MemoryData) SaveKnownHost(replace string, h config.KnownHost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if replace != "" {
		for i := range m.Hosts {
			if m.Hosts[i].Name == replace {
				m.Hosts[i] = h
				return nil
			}
		}
		return fmt.Errorf("no known host %q", replace)
	}
	m.Hosts = append(m.Hosts, h)
	return nil
}

func (m *MemoryData) RemoveKnownHost(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return false, m.Fail
	}
	n := len(m.Hosts)
	m.Hosts = slices.DeleteFunc(m.Hosts, func(h config.KnownHost) bool { return h.Name == name })
	return len(m.Hosts) != n, nil
}

// ErrInConfig is a change to a host that config.toml lists: it's changed by
// editing the file.
var ErrInConfig = errors.New("set in config.toml: edit the file to change it")

// knownHosts are every known host: config.toml's and the added ones (c.cfgMu
// is held).
func (c *Client) knownHosts() ([]config.KnownHost, error) {
	added, err := c.o.Data.KnownHosts()
	if err != nil {
		return nil, err
	}
	var hosts []config.KnownHost
	if c.o.Settings != nil {
		hosts = c.o.Settings().Hosts
	}
	return append(slices.Clone(hosts), added...), nil
}

func (c *Client) isInConfig(name string) bool {
	if c.o.Settings == nil {
		return false
	}
	return slices.ContainsFunc(c.o.Settings().Hosts, func(h config.KnownHost) bool { return h.Name == name })
}

// AddHost adds a known host. It's pinned by its fingerprint, given or read
// from its cert file.
func (c *Client) AddHost(e ipc.RemoteHostEdit) (ipc.RemoteHost, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	hosts, err := c.knownHosts()
	if err != nil {
		return ipc.RemoteHost{}, err
	}
	kh, err := checkHost(config.KnownHost{Name: e.Name, Address: e.Address, Fingerprint: e.Fingerprint, Cert: e.Cert}, hosts, "")
	if err != nil {
		return ipc.RemoteHost{}, err
	}
	if err := c.o.Data.SaveKnownHost("", kh); err != nil {
		return ipc.RemoteHost{}, err
	}
	if err := c.reloadLocked(); err != nil {
		return ipc.RemoteHost{}, err
	}
	return c.Host(kh.Name)
}

// EditHost changes a known host: the fields of e that are set. Renaming it
// moves its added sessions along; a new address or fingerprint reconnects.
func (c *Client) EditHost(_ context.Context, e ipc.RemoteHostEdit) (ipc.RemoteHost, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	hosts, err := c.knownHosts()
	if err != nil {
		return ipc.RemoteHost{}, err
	}
	var kh config.KnownHost
	found := false
	for _, h := range hosts {
		if h.Name == e.Host {
			kh, found = h, true
		}
	}
	if !found {
		return ipc.RemoteHost{}, wire.Errorf(wire.CodeNotFound, "there's no Remote host named %q", e.Host)
	}
	if c.isInConfig(e.Host) {
		return ipc.RemoteHost{}, fmt.Errorf("%q is %w", e.Host, ErrInConfig)
	}
	// From the host as the form loaded it: a change made since isn't lost.
	if e.Version != "" && e.Version != kh.Version() {
		return ipc.RemoteHost{}, wire.Errorf(api.CodeConflict, "%s changed since you opened it", e.Host)
	}
	if e.Name != "" {
		kh.Name = e.Name
	}
	if e.Address != "" {
		kh.Address = e.Address
	}
	if e.Fingerprint != "" || e.Cert != "" { // a new pin replaces both
		kh.Fingerprint, kh.Cert = e.Fingerprint, e.Cert
	}
	if kh, err = checkHost(kh, hosts, e.Host); err != nil {
		return ipc.RemoteHost{}, err
	}
	if kh.Name != e.Host {
		c.mu.Lock()
		for _, a := range c.added {
			if a.Host == e.Host {
				a.Host = kh.Name
			}
		}
		c.mu.Unlock()
	}
	if err := c.o.Data.SaveKnownHost(e.Host, kh); err != nil {
		return ipc.RemoteHost{}, err
	}
	if err := c.reloadLocked(); err != nil {
		return ipc.RemoteHost{}, err
	}
	return c.Host(kh.Name)
}

// RemoveHost forgets a known host, and its sessions go from here. Nothing on
// the host is touched.
func (c *Client) RemoveHost(_ context.Context, name string) error {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	if c.isInConfig(name) {
		return fmt.Errorf("%q is %w", name, ErrInConfig)
	}
	ok, err := c.o.Data.RemoveKnownHost(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("there's no Remote host named %q", name)
	}
	c.mu.Lock()
	for _, a := range c.sortedAddedLocked(name) {
		c.closeProxyLocked(a)
		c.inbox.set(a.ID, name, nil)
		delete(c.added, a.ID)
	}
	c.mu.Unlock()
	return c.reloadLocked()
}

// checkHost checks a host entry against the others (all but the one named
// replacing), and returns it as it's written: its fingerprint pinned from
// its cert when it has one.
func checkHost(kh config.KnownHost, hosts []config.KnownHost, replacing string) (config.KnownHost, error) {
	kh.Name = strings.TrimSpace(kh.Name)
	kh.Address = strings.TrimSpace(kh.Address)
	kh.Cert = strings.TrimSpace(kh.Cert)
	if kh.Name == "" {
		return kh, errors.New("a Remote host needs a name")
	}
	for _, h := range hosts {
		if h.Name == kh.Name && h.Name != replacing {
			return kh, fmt.Errorf("there's already a Remote host named %q", kh.Name)
		}
	}
	host, port, err := net.SplitHostPort(kh.Address)
	if n, perr := strconv.Atoi(port); err != nil || perr != nil || host == "" || n < 1 || n > 65535 {
		return kh, fmt.Errorf("the address is host:port, such as 192.168.1.20:7433, not %q", kh.Address)
	}
	if kh.Fingerprint == "" && kh.Cert == "" {
		return kh, errors.New("a Remote host needs its fingerprint, or its cert file")
	}
	if kh.Fingerprint != "" {
		if kh.Fingerprint, err = identity.ParseFingerprint(kh.Fingerprint); err != nil {
			return kh, err
		}
	}
	if kh.Cert != "" {
		fp, err := identity.FingerprintFromPEMFile(kh.Cert)
		if err != nil {
			return kh, fmt.Errorf("the host's cert: %w", err)
		}
		if kh.Fingerprint != "" && !identity.Equal(fp, kh.Fingerprint) {
			return kh, fmt.Errorf("the cert's fingerprint is %s, not %s", fp, kh.Fingerprint)
		}
		kh.Fingerprint = fp
	}
	return kh, nil
}
