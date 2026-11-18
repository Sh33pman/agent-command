package identity

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

// Prefix starts every fingerprint, as in OpenSSH's format.
const Prefix = "SHA256:"

// Fingerprint is a cert's identity: "SHA256:" and the base64 (no padding) of
// the SHA-256 of its DER.
func Fingerprint(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return Prefix + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ParseFingerprint checks a fingerprint someone typed or pasted and returns it
// in the form Fingerprint makes. The "SHA256:" prefix is optional, and base64
// padding is allowed.
func ParseFingerprint(s string) (string, error) {
	b, err := decode(s)
	if err != nil {
		return "", err
	}
	return Prefix + base64.RawStdEncoding.EncodeToString(b), nil
}

func decode(s string) ([]byte, error) {
	v := strings.TrimSpace(s)
	if len(v) >= len(Prefix) && strings.EqualFold(v[:len(Prefix)], Prefix) {
		v = v[len(Prefix):]
	}
	v = strings.TrimRight(v, "=")
	if v == "" {
		return nil, fmt.Errorf("fingerprint %q is empty: expected %s followed by 43 base64 characters", s, Prefix)
	}
	b, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("fingerprint %q isn't valid base64", s)
	}
	if len(b) != sha256.Size {
		return nil, fmt.Errorf("fingerprint %q is %d bytes, not %d: expected %s followed by 43 base64 characters", s, len(b), sha256.Size, Prefix)
	}
	return b, nil
}

// Equal says whether two fingerprints are the same, in constant time. An
// invalid fingerprint equals nothing.
func Equal(a, b string) bool {
	x, err := decode(a)
	if err != nil {
		return false
	}
	y, err := decode(b)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(x, y) == 1
}

// FingerprintFromPEMFile is the fingerprint of the first cert in a PEM file:
// how a peer is added by its cert file.
func FingerprintFromPEMFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a cert file the user named
	if err != nil {
		return "", err
	}
	certs, err := parseCerts(path, b)
	if err != nil {
		return "", err
	}
	return Fingerprint(certs[0].Raw), nil
}

// parseCerts reads the certs in a PEM file, the leaf first.
func parseCerts(path string, b []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: bad certificate: %w", path, err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%s: no PEM CERTIFICATE block found", path)
	}
	return certs, nil
}
