package wire

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// ServerTLS is the host's TLS: 1.3 only, showing id's cert, and asking every
// client for a cert of its own. Any cert that parses gets through the
// handshake: the host checks its fingerprint after the hello, so an unknown
// client can be told that its request is waiting for approval.
func ServerTLS(id *identity.Identity) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{id.Certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		// A resumed session would skip the client's cert.
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			_, err := leaf(raw)
			return err
		},
	}
}

// ClientTLS is the client's TLS: 1.3 only, showing id's cert, and accepting
// only a host whose cert has the pinned fingerprint. There's no CA, so Go's
// own chain check is off and the fingerprint is the whole check.
func ClientTLS(id *identity.Identity, pinned string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{id.Certificate},
		InsecureSkipVerify: true, //nolint:gosec // G402: the host is checked by its pinned fingerprint below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the host sent no certificate")
			}
			got := identity.Fingerprint(cs.PeerCertificates[0].Raw)
			if !identity.Equal(got, pinned) {
				return &FingerprintError{Want: pinned, Got: got}
			}
			return nil
		},
	}
}

func leaf(raw [][]byte) (*x509.Certificate, error) {
	if len(raw) == 0 {
		return nil, errors.New("no certificate")
	}
	return x509.ParseCertificate(raw[0])
}
