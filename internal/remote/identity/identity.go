// Package identity is this machine's identity for Remote sessions: a
// self-signed X.509 cert and its key, known to peers by the cert's
// fingerprint. acmd generates one the first time it's needed, and the config
// can name your own PEM files instead.
package identity

import (
	"crypto"
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
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The files of a generated identity, in its folder.
const (
	CertFile = "cert.pem"
	KeyFile  = "key.pem"
)

// Validity is how long a generated cert lasts. Peers pin the fingerprint, not
// a CA, so a long life only spares everyone a re-trust.
const Validity = 20 * 365 * 24 * time.Hour

// MinRSABits is the smallest RSA key accepted in your own certs.
const MinRSABits = 2048

// ErrKeyPermissions is a key file that others can read.
var ErrKeyPermissions = errors.New("permissions too open: others can read it")

// ErrMismatch is a cert and key that don't belong together.
var ErrMismatch = errors.New("the key doesn't belong to the cert")

// Identity is a cert and its key, ready for TLS.
type Identity struct {
	Certificate tls.Certificate   // for tls.Config.Certificates
	Leaf        *x509.Certificate // the cert itself
	CommonName  string            // the machine name, for a generated identity
	CertPath    string
	KeyPath     string
}

// Fingerprint is the cert's fingerprint, which peers pin.
func (id *Identity) Fingerprint() string { return Fingerprint(id.Leaf.Raw) }

// Files are the cert and key paths of the generated identity in dir.
func Files(dir string) (cert, key string) {
	return filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
}

// Exists says whether dir holds a generated identity (or part of one).
func Exists(dir string) bool {
	cert, key := Files(dir)
	for _, f := range []string{cert, key} {
		if _, err := os.Lstat(f); err == nil {
			return true
		}
	}
	return false
}

// Resolve is the identity a role uses: the generated one in defaultDir when
// the config leaves cert and key empty, otherwise the files it names.
func Resolve(cert, key, defaultDir string) (*Identity, error) {
	switch {
	case cert == "" && key == "":
		return Ensure(defaultDir)
	case cert == "" || key == "":
		return nil, errors.New("set both cert and key, or neither to use acmd's own identity")
	}
	return Load(cert, key)
}

// Ensure returns the generated identity in dir, making it the first time.
// The folder is 0700, the key 0600 and the cert 0644. Processes racing to make
// it agree on one: each builds a complete identity in a folder of its own and
// renames it into place, and whoever loses uses the winner's.
func Ensure(dir string) (*Identity, error) {
	if !Exists(dir) {
		tmp, err := build(dir)
		if err != nil {
			return nil, err
		}
		// Fails when another process got there first, which is fine. An
		// empty folder is in the way on macOS: it goes (os.Remove leaves a
		// folder with anything in it), and the rename is tried again.
		if os.Rename(tmp, dir) != nil && os.Remove(dir) == nil {
			_ = os.Rename(tmp, dir)
		}
		_ = os.RemoveAll(tmp)
	}
	return loadDir(dir)
}

// Regenerate replaces the generated identity in dir with a new one. Every
// peer has to trust the new fingerprint.
func Regenerate(dir string) (*Identity, error) {
	tmp, err := build(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	old := tmp + ".old"
	moved := os.Rename(dir, old) == nil
	if err := os.Rename(tmp, dir); err != nil {
		if moved {
			_ = os.Rename(old, dir)
		}
		return nil, err
	}
	_ = os.RemoveAll(old)
	return loadDir(dir)
}

func loadDir(dir string) (*Identity, error) {
	// Tighten a folder made by hand.
	if st, err := os.Stat(dir); err == nil && st.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a folder needs its execute bit; 0700 is owner-only
			return nil, err
		}
	}
	cert, key := Files(dir)
	for _, f := range []string{cert, key} {
		if _, err := os.Stat(f); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s is missing, so the identity in %s is incomplete: run `acmd remote identity --regenerate`", f, dir)
		}
	}
	return Load(cert, key)
}

// hostname is the machine's name; swappable in tests.
var hostname = os.Hostname

// machineName is the cert's common name: the machine's name without macOS's
// ".local".
func machineName() string {
	h, err := hostname()
	h = strings.TrimSuffix(strings.TrimSpace(h), ".local")
	if err != nil || h == "" {
		return "acmd"
	}
	return h
}

// build makes a new identity in a fresh folder next to dir, and returns that
// folder.
func build(dir string) (string, error) {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+"-*")
	if err != nil {
		return "", err
	}
	certPEM, keyPEM, err := generate(machineName(), time.Now())
	if err == nil {
		cert, key := Files(tmp)
		err = writeFile(key, keyPEM, 0o600)
		if err == nil {
			err = writeFile(cert, certPEM, 0o644)
		}
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	return tmp, nil
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, b, mode); err != nil {
		return err
	}
	// The umask may have taken bits off the cert's 0644.
	return os.Chmod(path, mode)
}

// generate makes a self-signed Ed25519 cert for name, and its key, as PEM.
func generate(name string, now time.Time) (certPEM, keyPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Agent Command"}},
		NotBefore:             now.Add(-time.Hour), // a peer's clock may be a little behind
		NotAfter:              now.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// Load reads a cert and key from PEM files: acmd's own or yours. It accepts
// Ed25519, ECDSA P-256 and RSA keys of 2048 bits or more. The key file must
// not be readable by others. Errors name the file at fault and the reason.
func Load(certPath, keyPath string) (*Identity, error) {
	certPEM, err := os.ReadFile(certPath) //nolint:gosec // G304: a cert file from the config
	if err != nil {
		return nil, err
	}
	certs, err := parseCerts(certPath, certPEM)
	if err != nil {
		return nil, err
	}
	leaf := certs[0]
	if err := checkPublicKey(leaf.PublicKey); err != nil {
		return nil, fmt.Errorf("%s: %w", certPath, err)
	}

	st, err := os.Stat(keyPath)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: %w (%s): run chmod 600 %s", keyPath, ErrKeyPermissions, st.Mode().Perm(), keyPath)
	}
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // G304: a key file from the config
	if err != nil {
		return nil, err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	pub, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(leaf.PublicKey) {
		return nil, fmt.Errorf("%s: %w in %s", keyPath, ErrMismatch, certPath)
	}

	chain := make([][]byte, len(certs))
	for i, c := range certs {
		chain[i] = c.Raw
	}
	return &Identity{
		Certificate: tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf},
		Leaf:        leaf,
		CommonName:  leaf.Subject.CommonName,
		CertPath:    certPath,
		KeyPath:     keyPath,
	}, nil
}

// checkPublicKey says whether a key is one acmd accepts.
func checkPublicKey(k any) error {
	switch k := k.(type) {
	case ed25519.PublicKey:
		return nil
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return fmt.Errorf("ECDSA key on curve %s: only P-256 is supported", k.Curve.Params().Name)
		}
		return nil
	case *rsa.PublicKey:
		if n := k.N.BitLen(); n < MinRSABits {
			return fmt.Errorf("RSA key is %d bits: it must be at least %d", n, MinRSABits)
		}
		return nil
	default:
		return fmt.Errorf("unsupported key type %T: use Ed25519, ECDSA P-256 or RSA %d+", k, MinRSABits)
	}
}

// parseKey reads the first private key in a PEM file: PKCS #8, or the older
// RSA (PKCS #1) and EC (SEC 1) forms.
func parseKey(b []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			return nil, errors.New("no PEM PRIVATE KEY block found")
		}
		var key any
		var err error
		switch block.Type {
		case "PRIVATE KEY":
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			key, err = x509.ParseECPrivateKey(block.Bytes)
		case "ENCRYPTED PRIVATE KEY":
			return nil, errors.New("the key is encrypted: acmd needs it without a passphrase")
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("bad private key: %w", err)
		}
		s, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("unsupported key type %T", key)
		}
		if err := checkPublicKey(s.Public()); err != nil {
			return nil, err
		}
		return s, nil
	}
}
