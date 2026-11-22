package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/version"
)

// remoteEnv starts a daemon that follows config.toml, with Hosting set to a
// free port, and returns its paths and that port.
func remoteEnv(t *testing.T) (paths.Paths, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	p := daemonEnv(t, func(p paths.Paths) {
		must(t, os.MkdirAll(p.ConfigDir, 0o700))
		must(t, os.WriteFile(p.Config(), []byte("[remote.host]\nport = "+strconv.Itoa(port)+"\n"), 0o600))
	})
	return p, port
}

// daemonEnv starts a daemon with its own folders and config.toml, after
// before (when set) has written what it starts with, and returns its paths.
func daemonEnv(t *testing.T, before ...func(paths.Paths)) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdr") // short: sockets have a length limit
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	t.Setenv("ACMD_CONFIG_DIR", p.ConfigDir)
	orig := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	t.Cleanup(func() { spawnDaemon = orig })
	for _, fn := range before {
		fn(p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: version.Get().Version, Log: log.New(io.Discard, "", 0),
			ConfigFile: p.Config(), TailEvery: time.Hour, ResolveEditor: resolveEditor})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; {
		c, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return p
}

func TestRemoteHostingCommands(t *testing.T) {
	p, port := remoteEnv(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	out := mustRun(t, "remote", "status")
	if !strings.Contains(out, "Hosting:     off (turn it on with: acmd remote hosting on)") || !strings.Contains(out, "Clients:     0 authorized, 0 connections") {
		t.Errorf("status off:\n%s", out)
	}
	if _, err := acmd(t, "remote", "hosting", "maybe"); err == nil {
		t.Error("hosting maybe")
	}
	out = mustRun(t, "remote", "hosting", "on")
	host, err := identity.Ensure(p.RemoteIdentityDir())
	must(t, err)
	for _, want := range []string{"Hosting:     on, listening on " + addr + "\n", "Fingerprint: " + host.Fingerprint() + "\n", "Name:        " + host.CommonName} {
		if !strings.Contains(out, want) {
			t.Errorf("hosting on lacks %q:\n%s", want, out)
		}
	}
	var st ipc.RemoteStatus
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "status", "--json")), &st))
	if !st.Listening || st.Port != port || st.Fingerprint != host.Fingerprint() {
		t.Errorf("status --json = %+v", st)
	}
	// identity shows where Hosting listens.
	if out := mustRun(t, "remote", "identity"); !strings.Contains(out, "Hosting on:  "+addr) {
		t.Errorf("identity:\n%s", out)
	}

	// An unknown client waits for approval.
	if out := mustRun(t, "remote", "requests"); !strings.Contains(out, "No requests waiting for approval.") {
		t.Errorf("no requests:\n%s", out)
	}
	laptop, err := identity.Ensure(filepath.Join(p.StateDir, "laptop"))
	must(t, err)
	dial := func() (*wire.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return wire.Dial(ctx, addr, laptop, host.Fingerprint(), wire.Hello{Version: "x"})
	}
	if _, err := dial(); !errors.Is(err, wire.ErrPending) {
		t.Fatalf("unknown client: %v", err)
	}
	out = mustRun(t, "remote", "requests")
	if !strings.Contains(out, laptop.Fingerprint()) || !strings.Contains(out, "acmd remote requests approve <id>") {
		t.Errorf("requests:\n%s", out)
	}
	if out := mustRun(t, "remote", "status"); !strings.Contains(out, "Requests:    1 waiting for approval") {
		t.Errorf("status with a request:\n%s", out)
	}
	var reqs []ipc.RemoteRequest
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "requests", "--json")), &reqs))
	if len(reqs) != 1 {
		t.Fatalf("requests --json = %+v", reqs)
	}
	out = mustRun(t, "remote", "requests", "approve", reqs[0].ID, "--name", "laptop")
	if out != "Approved request "+reqs[0].ID+`: "laptop" (`+laptop.Fingerprint()+") can connect now.\n" {
		t.Errorf("approve: %q", out)
	}
	if _, err := acmd(t, "remote", "requests", "deny", reqs[0].ID); err == nil {
		t.Error("denied an approved request")
	}
	c, err := dial()
	must(t, err)
	defer c.Close()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(mustRun(t, "remote", "clients"), "c1: from 127.0.0.1:"); {
		if time.Now().After(deadline) {
			t.Fatalf("clients:\n%s", mustRun(t, "remote", "clients"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	var clients []ipc.RemoteClient
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "clients", "--json")), &clients))
	if len(clients) != 1 || clients[0].Name != "laptop" || len(clients[0].Connections) != 1 {
		t.Errorf("clients --json = %+v", clients)
	}
	if out := mustRun(t, "remote", "disconnect", "laptop"); out != "Disconnected laptop: closed 1 connection.\n" {
		t.Errorf("disconnect: %q", out)
	}
	if _, err := acmd(t, "remote", "disconnect", "laptop"); err == nil {
		t.Error("disconnected twice")
	}

	// Authorize by cert file (named after its cert) or fingerprint, and revoke.
	desk, err := identity.Ensure(filepath.Join(p.StateDir, "desk"))
	must(t, err)
	if out := mustRun(t, "remote", "authorize", desk.CertPath); out != `Authorized "`+desk.CommonName+`-2" (`+desk.Fingerprint()+"): it can connect now.\n" &&
		out != `Authorized "`+desk.CommonName+`" (`+desk.Fingerprint()+"): it can connect now.\n" {
		t.Errorf("authorize a cert: %q", out)
	}
	third, err := identity.Ensure(filepath.Join(p.StateDir, "third"))
	must(t, err)
	if out := mustRun(t, "remote", "authorize", third.Fingerprint(), "--name", "third"); !strings.Contains(out, `Authorized "third"`) {
		t.Errorf("authorize a fingerprint: %q", out)
	}
	if _, err := acmd(t, "remote", "authorize", "not-a-thing"); err == nil || !strings.Contains(err.Error(), "neither a cert file nor a fingerprint") {
		t.Errorf("authorize junk: %v", err)
	}
	if out := mustRun(t, "remote", "revoke", "third"); !strings.Contains(out, `Revoked "third" (`+third.Fingerprint()+")") {
		t.Errorf("revoke: %q", out)
	}
	var authorized []ipc.RemoteClient
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "clients", "--json")), &authorized))
	if len(authorized) != 2 {
		t.Errorf("authorized = %+v", authorized)
	}

	// A request that's denied.
	if _, err := identityDial(addr, host, filepath.Join(p.StateDir, "fourth")); !errors.Is(err, wire.ErrPending) {
		t.Fatal(err)
	}
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "requests", "--json")), &reqs))
	if out := mustRun(t, "remote", "requests", "deny", reqs[0].ID); out != "Denied request "+reqs[0].ID+".\n" {
		t.Errorf("deny: %q", out)
	}

	out = mustRun(t, "remote", "hosting", "off")
	if !strings.Contains(out, "Hosting:     off") {
		t.Errorf("hosting off:\n%s", out)
	}
	if out := mustRun(t, "remote", "requests", "--json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("no requests as JSON: %q", out)
	}
}

func identityDial(addr string, host *identity.Identity, dir string) (*wire.Conn, error) {
	id, err := identity.Ensure(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wire.Dial(ctx, addr, id, host.Fingerprint(), wire.Hello{})
}

func TestRemoteOutput(t *testing.T) {
	var b strings.Builder
	writeRemoteStatus(&b, ipc.RemoteStatus{Enabled: true, Error: "port 7433 is in use", Authorized: 1, Connected: 1})
	writeRemoteStatus(&b, ipc.RemoteStatus{Enabled: true, Listening: true, Address: "127.0.0.1:50000", Port: 50000, ConfiguredPort: 7433})
	for _, want := range []string{"Hosting:     on, but not listening: port 7433 is in use\n", "Clients:     1 authorized, 1 connection\n",
		"Hosting:     on, listening on 127.0.0.1:50000 (port 7433 was taken)\n"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	writeRemoteClients(&b, nil)
	since := time.Date(2026, 11, 18, 9, 0, 0, 0, time.Local)
	writeRemoteClients(&b, []ipc.RemoteClient{
		{Name: "laptop", Fingerprint: "SHA256:a", Scope: "act", Connections: []ipc.RemoteConnection{
			{ID: "c1", Addr: "10.0.0.2:5000", Since: since, Sessions: []string{"s1", "s2"}},
			{ID: "c2", Addr: "10.0.0.2:5001", Since: since}}},
		{Name: "desk", Fingerprint: "SHA256:b", Scope: "admin"}})
	for _, want := range []string{"No authorized clients.", "NAME    MAY    FINGERPRINT", "laptop  act    SHA256:a     c1: from 10.0.0.2:5000 since 2026-11-18 09:00, attached to s1, s2\n",
		"c2: from 10.0.0.2:5001", "desk    admin  SHA256:b     -\n"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("clients lack %q:\n%s", want, b.String())
		}
	}
	if certName("/nowhere") != "" {
		t.Error("certName of a missing file")
	}
	f := filepath.Join(t.TempDir(), "x.pem")
	must(t, os.WriteFile(f, []byte("-----BEGIN JUNK-----\nAAAA\n-----END JUNK-----\n-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o600))
	if certName(f) != "" {
		t.Error("certName of a bad cert")
	}
	if _, _, err := clientFingerprint(f); err == nil {
		t.Error("fingerprint of a bad cert")
	}
}

// TestRemoteIdentityRoles: with its own cert for one role, identity shows both.
func TestRemoteIdentityRoles(t *testing.T) {
	cfgDir := isolate(t)
	own, err := identity.Ensure(filepath.Join(t.TempDir(), "own"))
	must(t, err)
	file := filepath.Join(cfgDir, "config.toml")
	must(t, writeSetting(file, "remote.client", "cert", own.CertPath))
	must(t, writeSetting(file, "remote.client", "key", own.KeyPath))
	out := mustRun(t, "remote", "identity")
	gen, err := identity.Ensure(filepath.Join(cfgDir, "identity"))
	must(t, err)
	for _, want := range []string{"Fingerprint: " + gen.Fingerprint() + "\n", "Used for:    Hosting ([remote.host])",
		"Fingerprint: " + own.Fingerprint() + "\n", "Used for:    connecting to hosts ([remote.client])"} {
		if !strings.Contains(out, want) {
			t.Errorf("identity lacks %q:\n%s", want, out)
		}
	}
	var got identityJSON
	must(t, json.Unmarshal([]byte(mustRun(t, "remote", "identity", "--json")), &got))
	if got.Fingerprint != gen.Fingerprint() || strings.Join(got.UsedFor, ",") != "hosting" || got.Client == nil || got.Client.Fingerprint != own.Fingerprint() {
		t.Errorf("--json = %+v", got)
	}
	// Half a cert is an error that names the role.
	must(t, writeSetting(file, "remote.client", "key", ""))
	if _, err := acmd(t, "remote", "identity"); err == nil || !strings.Contains(err.Error(), "[remote.client]") {
		t.Errorf("half a cert: %v", err)
	}
	must(t, writeSetting(file, "remote.host", "key", "/x"))
	if _, err := acmd(t, "remote", "identity"); err == nil || !strings.Contains(err.Error(), "[remote.host]") {
		t.Errorf("half a host cert: %v", err)
	}
	must(t, os.WriteFile(file, []byte("x = "), 0o600))
	if _, err := acmd(t, "remote", "identity"); err == nil {
		t.Error("a bad config.toml")
	}
}
