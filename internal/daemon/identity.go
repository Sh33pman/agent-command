package daemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// This machine's identity for Remote sessions, for the apps: what
// `acmd remote identity` shows, without a process to run.

// remoteIdentity resolves both roles' identities from the settings: one and
// the same unless the settings name a cert of your own for one.
func (d *Daemon) remoteIdentity() (ipc.RemoteIdentity, error) {
	r := d.config().Remote
	dir := d.cfg.Paths.RemoteIdentityDir()
	host, err := identity.Resolve(r.Host.Cert, r.Host.Key, dir)
	if err != nil {
		return ipc.RemoteIdentity{}, fmt.Errorf("the Hosting identity ([remote.host]): %w", err)
	}
	client, err := identity.Resolve(r.Client.Cert, r.Client.Key, dir)
	if err != nil {
		return ipc.RemoteIdentity{}, fmt.Errorf("the client identity ([remote.client]): %w", err)
	}
	of := func(id *identity.Identity, usedFor ...string) ipc.RemoteIdentity {
		return ipc.RemoteIdentity{Fingerprint: id.Fingerprint(), CommonName: id.CommonName, Cert: id.CertPath, Key: id.KeyPath, UsedFor: usedFor}
	}
	var out ipc.RemoteIdentity
	if host.CertPath == client.CertPath && host.Fingerprint() == client.Fingerprint() {
		out = of(host, "hosting", "client")
	} else {
		out = of(host, "hosting")
		c := of(client, "client")
		out.Client = &c
	}
	if st := d.remote.Status(); st.Listening {
		out.Address = st.Address
	}
	return out, nil
}

// regenerate makes a new identity, and Hosting and the hosts use it from now
// on. Every peer that trusts this machine must trust the new fingerprint.
func (d *Daemon) regenerate() (ipc.RemoteIdentity, error) {
	if _, err := identity.Regenerate(d.cfg.Paths.RemoteIdentityDir()); err != nil {
		return ipc.RemoteIdentity{}, err
	}
	d.followSettings()
	d.changes.bump()
	return d.remoteIdentity()
}

// certRoles are the sections that name a cert and key of your own.
var certRoles = map[string]string{"host": "remote.host", "client": "remote.client"}

// setCert names a cert and key of your own for a role, once they're checked
// to belong together, or goes back to acmd's identity.
func (d *Daemon) setCert(c ipc.RemoteCert) (ipc.RemoteIdentity, error) {
	section, ok := certRoles[c.Role]
	if !ok {
		return ipc.RemoteIdentity{}, fmt.Errorf("say host or client, not %q", c.Role)
	}
	cert, key := strings.TrimSpace(c.Cert), strings.TrimSpace(c.Key)
	switch {
	case c.Default:
		cert, key = "", ""
	case cert == "" || key == "":
		return ipc.RemoteIdentity{}, errors.New("give a cert file and a key file, or use acmd's identity")
	default:
		if !filepath.IsAbs(cert) || !filepath.IsAbs(key) {
			return ipc.RemoteIdentity{}, errors.New("the cert and key are full paths")
		}
		if _, err := identity.Load(cert, key); err != nil {
			return ipc.RemoteIdentity{}, err
		}
	}
	if _, err := d.setConfig(ipc.ConfigSet{Values: map[string]any{section + ".cert": cert, section + ".key": key}}); err != nil {
		return ipc.RemoteIdentity{}, err
	}
	return d.remoteIdentity()
}

// clientByCert is the fingerprint, and the name, a client's cert file gives
// remote.authorize.
func clientByCert(ref ipc.RemoteRef) (ipc.RemoteRef, error) {
	if ref.Cert == "" {
		return ref, nil
	}
	fp, err := identity.FingerprintFromPEMFile(ref.Cert)
	if err != nil {
		return ref, err
	}
	ref.Fingerprint = fp
	if ref.Name == "" {
		ref.Name = identity.CommonNameFromPEMFile(ref.Cert)
	}
	return ref, nil
}
