package identity

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	must(t, err)
	return st.Mode().Perm()
}

func withHostname(t *testing.T, name string, err error) {
	t.Helper()
	old := hostname
	hostname = func() (string, error) { return name, err }
	t.Cleanup(func() { hostname = old })
}

func TestEnsureGeneratesOnce(t *testing.T) {
	withHostname(t, "studio-mac.local", nil)
	dir := filepath.Join(t.TempDir(), "config", "identity")
	id, err := Ensure(dir)
	must(t, err)
	cert, key := Files(dir)
	if id.CertPath != cert || id.KeyPath != key {
		t.Errorf("paths %s %s, want %s %s", id.CertPath, id.KeyPath, cert, key)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, key: 0o600, cert: 0o644} {
		if got := perm(t, path); got != want {
			t.Errorf("%s is %o, want %o", path, got, want)
		}
	}
	if id.CommonName != "studio-mac" || id.Leaf.Subject.CommonName != "studio-mac" {
		t.Errorf("common name %q, want studio-mac", id.CommonName)
	}
	if _, ok := id.Leaf.PublicKey.(ed25519.PublicKey); !ok {
		t.Errorf("key is %T, want Ed25519", id.Leaf.PublicKey)
	}
	if life := id.Leaf.NotAfter.Sub(id.Leaf.NotBefore); life < 19*365*24*time.Hour {
		t.Errorf("cert lasts only %s", life)
	}
	if id.Certificate.Leaf != id.Leaf || len(id.Certificate.Certificate) != 1 || id.Certificate.PrivateKey == nil {
		t.Errorf("tls.Certificate not filled in: %+v", id.Certificate)
	}
	if !strings.HasPrefix(id.Fingerprint(), Prefix) || id.Fingerprint() != Fingerprint(id.Leaf.Raw) {
		t.Errorf("fingerprint %q", id.Fingerprint())
	}

	again, err := Ensure(dir)
	must(t, err)
	if again.Fingerprint() != id.Fingerprint() {
		t.Error("a second Ensure made a new identity")
	}
	// Nothing is left behind next to the folder.
	entries, err := os.ReadDir(filepath.Dir(dir))
	must(t, err)
	if len(entries) != 1 {
		t.Errorf("stray files next to the identity: %v", entries)
	}
}

func TestMachineName(t *testing.T) {
	for _, tc := range []struct {
		host string
		err  error
		want string
	}{
		{"box", nil, "box"},
		{"Brett-MacBook.local", nil, "Brett-MacBook"},
		{"", nil, "acmd"},
		{"x", errors.New("no name"), "acmd"},
	} {
		withHostname(t, tc.host, tc.err)
		if got := machineName(); got != tc.want {
			t.Errorf("hostname %q (%v): %q, want %q", tc.host, tc.err, got, tc.want)
		}
	}
}

func TestEnsureUsesAnEmptyFolderAndTightensIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	must(t, os.Mkdir(dir, 0o755))
	must(t, os.Chmod(dir, 0o755))
	_, err := Ensure(dir)
	must(t, err)
	if got := perm(t, dir); got != 0o700 {
		t.Errorf("folder is %o, want 700", got)
	}
}

func TestEnsureRefusesAnIncompleteIdentity(t *testing.T) {
	dir := t.TempDir()
	cert, key := Files(dir)
	must(t, os.WriteFile(cert, []byte("x"), 0o644))
	_, err := Ensure(dir)
	if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "--regenerate") {
		t.Errorf("err = %v, want one naming %s", err, key)
	}
}

func TestEnsureFailsWhenTheFolderCantBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(file, nil, 0o600))
	if _, err := Ensure(filepath.Join(file, "identity")); err == nil {
		t.Error("made an identity under a file")
	}
}

// Processes racing to make the identity all end up with the same one.
func TestEnsureRace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	const n = 16
	fps := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := Ensure(dir)
			errs[i] = err
			if err == nil {
				fps[i] = id.Fingerprint()
			}
		}()
	}
	close(start)
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Ensure %d: %v", i, errs[i])
		}
		if fps[i] != fps[0] {
			t.Fatalf("Ensure %d got %s, Ensure 0 got %s", i, fps[i], fps[0])
		}
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	must(t, err)
	if len(entries) != 1 {
		t.Errorf("stray files next to the identity: %v", entries)
	}
	id, err := Ensure(dir)
	must(t, err)
	if id.Fingerprint() != fps[0] {
		t.Error("the identity on disk isn't the one everyone got")
	}
}

func TestRegenerate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	// With nothing there yet, it just makes one.
	first, err := Regenerate(dir)
	must(t, err)
	second, err := Regenerate(dir)
	must(t, err)
	if first.Fingerprint() == second.Fingerprint() {
		t.Error("Regenerate kept the old identity")
	}
	loaded, err := Ensure(dir)
	must(t, err)
	if loaded.Fingerprint() != second.Fingerprint() {
		t.Error("the new identity isn't the one on disk")
	}
	_, key := Files(dir)
	if got := perm(t, key); got != 0o600 {
		t.Errorf("key is %o", got)
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	must(t, err)
	if len(entries) != 1 {
		t.Errorf("stray files next to the identity: %v", entries)
	}
}

func TestRegenerateFailsWhenTheFolderCantBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(file, nil, 0o600))
	if _, err := Regenerate(filepath.Join(file, "identity")); err == nil {
		t.Error("made an identity under a file")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Error("an empty folder has an identity")
	}
	_, key := Files(dir)
	must(t, os.WriteFile(key, nil, 0o600))
	if !Exists(dir) {
		t.Error("half an identity should count")
	}
}

// pair writes a self-signed cert for signer, and its key in the given PEM
// form ("pkcs8", "pkcs1" or "ec"), and returns their paths.
func pair(t *testing.T, signer crypto.Signer, form string) (certPath, keyPath string) {
	t.Helper()
	return pairFor(t, signer, signer, form)
}

// pairFor is pair with the cert for one key and the key file for another.
func pairFor(t *testing.T, certKey, fileKey crypto.Signer, form string) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	der := selfSigned(t, certKey)
	must(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644))
	var block *pem.Block
	switch form {
	case "pkcs8":
		b, err := x509.MarshalPKCS8PrivateKey(fileKey)
		must(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: b}
	case "pkcs1":
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(fileKey.(*rsa.PrivateKey))}
	case "ec":
		b, err := x509.MarshalECPrivateKey(fileKey.(*ecdsa.PrivateKey))
		must(t, err)
		block = &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	}
	// Something before the key, as some tools write, is skipped.
	b := append(pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{1}}), pem.EncodeToMemory(block)...)
	must(t, os.WriteFile(keyPath, b, 0o600))
	return certPath, keyPath
}

func selfSigned(t *testing.T, key crypto.Signer) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mine"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	must(t, err)
	return der
}

func edKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	must(t, err)
	return k
}

func ecKey(t *testing.T, c elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	must(t, err)
	return k
}

func rsaKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	must(t, err)
	return k
}

func TestLoadAcceptsEachKeyType(t *testing.T) {
	rsa2048 := rsaKey(t, 2048)
	p256 := ecKey(t, elliptic.P256())
	for _, tc := range []struct {
		name string
		key  crypto.Signer
		form string
	}{
		{"Ed25519", edKey(t), "pkcs8"},
		{"ECDSA P-256 PKCS #8", p256, "pkcs8"},
		{"ECDSA P-256 SEC 1", p256, "ec"},
		{"RSA 2048 PKCS #8", rsa2048, "pkcs8"},
		{"RSA 2048 PKCS #1", rsa2048, "pkcs1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, key := pair(t, tc.key, tc.form)
			id, err := Load(cert, key)
			must(t, err)
			if id.CommonName != "mine" || id.CertPath != cert || id.KeyPath != key {
				t.Errorf("identity %+v", id)
			}
			fp, err := FingerprintFromPEMFile(cert)
			must(t, err)
			if fp != id.Fingerprint() {
				t.Errorf("FingerprintFromPEMFile %s, identity %s", fp, id.Fingerprint())
			}
		})
	}
}

func TestLoadKeepsTheChain(t *testing.T) {
	cert, key := pair(t, edKey(t), "pkcs8")
	b, err := os.ReadFile(cert)
	must(t, err)
	extra := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: selfSigned(t, edKey(t))})
	must(t, os.WriteFile(cert, append(b, extra...), 0o644))
	id, err := Load(cert, key)
	must(t, err)
	if len(id.Certificate.Certificate) != 2 || id.Fingerprint() != Fingerprint(id.Certificate.Certificate[0]) {
		t.Errorf("chain of %d, leaf first?", len(id.Certificate.Certificate))
	}
}

func writeFileT(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	must(t, os.WriteFile(path, []byte(content), mode))
	must(t, os.Chmod(path, mode))
}

func TestLoadRejections(t *testing.T) {
	x25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	must(t, err)
	x25519DER, err := x509.MarshalPKCS8PrivateKey(x25519)
	must(t, err)
	pemOf := func(typ string, b []byte) string { return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b})) }

	for _, tc := range []struct {
		name string
		// setup makes the files; it returns the paths and the file the
		// error must name.
		setup func(t *testing.T) (cert, key, blame string)
		want  string
	}{
		{"missing cert", func(t *testing.T) (string, string, string) {
			_, key := pair(t, edKey(t), "pkcs8")
			missing := filepath.Join(t.TempDir(), "nope.pem")
			return missing, key, missing
		}, "no such file"},
		{"missing key", func(t *testing.T) (string, string, string) {
			cert, _ := pair(t, edKey(t), "pkcs8")
			missing := filepath.Join(t.TempDir(), "nope.pem")
			return cert, missing, missing
		}, "no such file"},
		{"cert isn't PEM", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, cert, "hello", 0o644)
			return cert, key, cert
		}, "no PEM CERTIFICATE block"},
		{"cert is garbage", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, cert, pemOf("CERTIFICATE", []byte("junk")), 0o644)
			return cert, key, cert
		}, "bad certificate"},
		{"weak RSA cert", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, rsaKey(t, 1024), "pkcs1")
			return cert, key, cert
		}, "RSA key is 1024 bits"},
		{"ECDSA P-384 cert", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, ecKey(t, elliptic.P384()), "ec")
			return cert, key, cert
		}, "only P-256"},
		{"key readable by others", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			must(t, os.Chmod(key, 0o644))
			return cert, key, key
		}, "permissions too open"},
		{"key readable by the group", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			must(t, os.Chmod(key, 0o640))
			return cert, key, key
		}, "chmod 600"},
		{"key isn't PEM", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, key, "hello", 0o600)
			return cert, key, key
		}, "no PEM PRIVATE KEY block"},
		{"key is garbage", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, key, pemOf("PRIVATE KEY", []byte("junk")), 0o600)
			return cert, key, key
		}, "bad private key"},
		{"encrypted key", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, key, pemOf("ENCRYPTED PRIVATE KEY", []byte("x")), 0o600)
			return cert, key, key
		}, "encrypted"},
		{"key that can't sign", func(t *testing.T) (string, string, string) {
			cert, key := pair(t, edKey(t), "pkcs8")
			writeFileT(t, key, pemOf("PRIVATE KEY", x25519DER), 0o600)
			return cert, key, key
		}, "unsupported key type"},
		{"weak RSA key", func(t *testing.T) (string, string, string) {
			cert, key := pairFor(t, rsaKey(t, 2048), rsaKey(t, 1024), "pkcs1")
			return cert, key, key
		}, "RSA key is 1024 bits"},
		{"mismatched pair", func(t *testing.T) (string, string, string) {
			cert, key := pairFor(t, edKey(t), edKey(t), "pkcs8")
			return cert, key, key
		}, "doesn't belong to the cert"},
		{"mismatched key types", func(t *testing.T) (string, string, string) {
			cert, key := pairFor(t, edKey(t), ecKey(t, elliptic.P256()), "ec")
			return cert, key, key
		}, "doesn't belong to the cert"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, key, blame := tc.setup(t)
			_, err := Load(cert, key)
			if err == nil {
				t.Fatal("loaded")
			}
			if !strings.Contains(err.Error(), blame) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %s and say %q", err, blame, tc.want)
			}
		})
	}
}

func TestLoadErrorsCanBeTold(t *testing.T) {
	cert, key := pair(t, edKey(t), "pkcs8")
	must(t, os.Chmod(key, 0o604))
	if _, err := Load(cert, key); !errors.Is(err, ErrKeyPermissions) {
		t.Errorf("err = %v, want ErrKeyPermissions", err)
	}
	cert, key = pairFor(t, edKey(t), edKey(t), "pkcs8")
	if _, err := Load(cert, key); !errors.Is(err, ErrMismatch) {
		t.Errorf("err = %v, want ErrMismatch", err)
	}
}

func TestCheckPublicKeyRejectsOtherTypes(t *testing.T) {
	if err := checkPublicKey("not a key"); err == nil || !strings.Contains(err.Error(), "unsupported key type") {
		t.Errorf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	gen, err := Resolve("", "", dir)
	must(t, err)
	if !Exists(dir) || gen.CertPath != filepath.Join(dir, CertFile) {
		t.Errorf("empty config should use the generated identity: %+v", gen)
	}
	cert, key := pair(t, ecKey(t, elliptic.P256()), "pkcs8")
	own, err := Resolve(cert, key, dir)
	must(t, err)
	if own.CertPath != cert || own.Fingerprint() == gen.Fingerprint() {
		t.Errorf("config files ignored: %+v", own)
	}
	for _, args := range [][2]string{{cert, ""}, {"", key}} {
		if _, err := Resolve(args[0], args[1], dir); err == nil || !strings.Contains(err.Error(), "both cert and key") {
			t.Errorf("Resolve(%q, %q): %v", args[0], args[1], err)
		}
	}
}

// A generated identity works for mutual TLS 1.3, pinned by fingerprint.
func TestGeneratedIdentityDoesMutualTLS(t *testing.T) {
	host, err := Ensure(filepath.Join(t.TempDir(), "host"))
	must(t, err)
	client, err := Ensure(filepath.Join(t.TempDir(), "client"))
	must(t, err)
	pin := func(want string) func([][]byte, [][]*x509.Certificate) error {
		return func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || !Equal(Fingerprint(raw[0]), want) {
				return errors.New("fingerprint mismatch")
			}
			return nil
		}
	}
	a, b := net.Pipe()
	srv := tls.Server(a, &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{host.Certificate},
		ClientAuth: tls.RequireAnyClientCert, VerifyPeerCertificate: pin(client.Fingerprint()),
	})
	cli := tls.Client(b, &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{client.Certificate},
		InsecureSkipVerify: true, VerifyPeerCertificate: pin(host.Fingerprint()),
	})
	done := make(chan error, 1)
	go func() { done <- srv.Handshake() }()
	must(t, cli.Handshake())
	must(t, <-done)
	if cli.ConnectionState().Version != tls.VersionTLS13 {
		t.Error("not TLS 1.3")
	}
	_ = cli.Close()
	_ = srv.Close()
}
