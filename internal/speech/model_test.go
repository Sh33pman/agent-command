package speech

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modelArchive(t *testing.T, names ...string) (data []byte, sum string) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := []byte("data of " + name)
		must(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}))
		_, err := tw.Write(body)
		must(t, err)
	}
	must(t, tw.Close())
	must(t, gz.Close())
	h := sha256.Sum256(b.Bytes())
	return b.Bytes(), hex.EncodeToString(h[:])
}

func hostedEngine(t *testing.T, data []byte, sum string) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	t.Cleanup(srv.Close)
	e := testEngine(t)
	must(t, os.RemoveAll(e.modelUnpacked()))
	e.ModelURL, e.ModelSHA256 = srv.URL, sum
	e.Helper = fakeHelper(t, t.TempDir(), "exit 0")
	return e
}

func TestInstallDownloadsTheHostedModel(t *testing.T) {
	data, sum := modelArchive(t, "NOTICE", "mlx-audio/repo/config.json")
	e := hostedEngine(t, data, sum)
	var out bytes.Buffer
	must(t, e.Install(context.Background(), &out))
	b, err := os.ReadFile(filepath.Join(e.modelUnpacked(), "repo", "config.json"))
	if err != nil || string(b) != "data of mlx-audio/repo/config.json" {
		t.Errorf("model file: %q, %v", b, err)
	}
	if e.Status() != "" {
		t.Errorf("status %q", e.Status())
	}
	for _, leftover := range []string{"model.tar.gz.part", "mlx-audio.part"} {
		if _, err := os.Lstat(filepath.Join(e.home(), leftover)); err == nil {
			t.Errorf("%s is still there", leftover)
		}
	}
}

func TestInstallReportsProgress(t *testing.T) {
	data, sum := modelArchive(t, "mlx-audio/repo/config.json")
	e := hostedEngine(t, data, sum)
	var seen []int
	e.Progress = func(percent int) { seen = append(seen, percent) }
	must(t, e.Install(context.Background(), &bytes.Buffer{}))
	if len(seen) == 0 || seen[len(seen)-1] != 100 {
		t.Errorf("progress %v", seen)
	}
}

func TestInstallDoesntDownloadTwice(t *testing.T) {
	e := hostedEngine(t, nil, "")
	e.ModelURL = "http://127.0.0.1:1/nothing-here"
	must(t, os.MkdirAll(e.modelUnpacked(), 0o700))
	must(t, e.downloadModel(context.Background()))
}

func TestInstallRejectsAWrongChecksum(t *testing.T) {
	data, _ := modelArchive(t, "mlx-audio/repo/config.json")
	e := hostedEngine(t, data, "0000")
	err := e.Install(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(e.modelUnpacked()); err == nil {
		t.Error("unpacked a model that failed its checksum")
	}
	if e.Status() != "not installed" {
		t.Errorf("status %q", e.Status())
	}
}

func TestUnpackStaysInItsFolder(t *testing.T) {
	data, _ := modelArchive(t, "../escaped")
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	must(t, os.WriteFile(archive, data, 0o600))
	dir := filepath.Join(t.TempDir(), "in", "here")
	if err := unpack(archive, dir); err == nil {
		t.Fatal("unpacked a path outside its folder")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped")); err == nil {
		t.Error("the file escaped")
	}
}
