package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Known hosts are [[remote.hosts]] in config.toml. These change them,
// keeping the rest of the file and its comments, and follow the change at
// once.

// knownHosts reads [[remote.hosts]] from config.toml (c.cfgMu is held).
func (c *Client) knownHosts() ([]config.KnownHost, error) {
	if c.o.ConfigFile == "" {
		return nil, errors.New("no config.toml to keep Remote hosts in")
	}
	cfg, err := config.Load(c.o.ConfigFile)
	if err != nil {
		return nil, err
	}
	return cfg.Remote.Hosts, nil
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
	if err := setup.SetKnownHost(c.o.ConfigFile, "", kh); err != nil {
		return ipc.RemoteHost{}, err
	}
	if err := c.reloadLocked(true); err != nil {
		return ipc.RemoteHost{}, err
	}
	return c.Host(kh.Name)
}

// EditHost changes a known host: the fields of e that are set. Renaming it
// moves its added sessions along; a new address or fingerprint reconnects.
func (c *Client) EditHost(ctx context.Context, e ipc.RemoteHostEdit) (ipc.RemoteHost, error) {
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
		return ipc.RemoteHost{}, fmt.Errorf("there's no Remote host named %q", e.Host)
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
		if err := c.b.RenameHost(ctx, e.Host, kh.Name); err != nil {
			return ipc.RemoteHost{}, err
		}
		c.mu.Lock()
		for _, a := range c.added {
			if a.Host == e.Host {
				a.Host = kh.Name
			}
		}
		c.mu.Unlock()
	}
	if err := setup.SetKnownHost(c.o.ConfigFile, e.Host, kh); err != nil {
		return ipc.RemoteHost{}, err
	}
	if err := c.reloadLocked(true); err != nil {
		return ipc.RemoteHost{}, err
	}
	return c.Host(kh.Name)
}

// RemoveHost forgets a known host, and removes the sessions added from it
// here. Nothing on the host is touched.
func (c *Client) RemoveHost(ctx context.Context, name string) error {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	n, err := setup.RemoveKnownHost(c.o.ConfigFile, name)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("there's no Remote host named %q", name)
	}
	c.mu.Lock()
	gone := c.sortedAddedLocked(name)
	c.mu.Unlock()
	var errs []error
	for _, a := range gone {
		if err := c.b.Remove(ctx, a.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		c.forget(a.ID)
	}
	errs = append(errs, c.reloadLocked(true))
	return errors.Join(errs...)
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
