package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha256("abc"), base64 without padding.
const abc = "SHA256:ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0"

func TestFingerprint(t *testing.T) {
	if got := Fingerprint([]byte("abc")); got != abc {
		t.Errorf("Fingerprint = %s, want %s", got, abc)
	}
	if len(abc) != len(Prefix)+43 {
		t.Errorf("unexpected length %d", len(abc))
	}
}

func TestParseFingerprint(t *testing.T) {
	bare := strings.TrimPrefix(abc, Prefix)
	for _, in := range []string{abc, bare, "sha256:" + bare, "  " + abc + "\n", abc + "="} {
		got, err := ParseFingerprint(in)
		if err != nil || got != abc {
			t.Errorf("ParseFingerprint(%q) = %q, %v", in, got, err)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"", "empty"},
		{"SHA256:", "empty"},
		{"SHA256:not base64!", "valid base64"},
		{"SHA256:" + bare[:20], "not 32"},
		{"MD5:" + bare, "valid base64"},
	} {
		if _, err := ParseFingerprint(tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseFingerprint(%q): %v, want %q", tc.in, err, tc.want)
		}
	}
}

func TestEqual(t *testing.T) {
	bare := strings.TrimPrefix(abc, Prefix)
	other := Fingerprint([]byte("abd"))
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{abc, abc, true},
		{abc, bare, true},
		{"sha256:" + bare, abc, true},
		{abc, other, false},
		{abc, "junk", false},
		{"junk", abc, false},
		{"", "", false},
	} {
		if got := Equal(tc.a, tc.b); got != tc.want {
			t.Errorf("Equal(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestFingerprintFromPEMFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.pem")
	if _, err := FingerprintFromPEMFile(missing); err == nil {
		t.Error("read a missing file")
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	must(t, os.WriteFile(bad, []byte("not pem"), 0o644))
	if _, err := FingerprintFromPEMFile(bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("err = %v, want it to name %s", err, bad)
	}
}
