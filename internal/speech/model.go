package speech

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The model and its text-processing resources, hosted by us at a fixed version
// (scripts/package-speech-model.sh). The checksum is what makes it safe to unpack.
const (
	modelURL    = "https://github.com/brettmostert/homebrew-tap/releases/download/speech-model-v1/speech-model-v1.tar.gz"
	modelSHA256 = "ff010e7ea795fd8001531a0c9467cb0bdf353f23d99f08bb9d5ceb5993b10e28"
)

// modelUnpacked is where the archive's files end up: what the helper reads.
func (e *Engine) modelUnpacked() string { return filepath.Join(e.home(), "mlx-audio") }

// downloadModel fetches the model into e.home(), unless a previous install did.
func (e *Engine) downloadModel(ctx context.Context) error {
	if _, err := os.Stat(e.modelUnpacked()); err == nil {
		return nil
	}
	url, want := modelURL, modelSHA256
	if e.ModelURL != "" {
		url, want = e.ModelURL, e.ModelSHA256
	}
	archive := filepath.Join(e.home(), "model.tar.gz.part")
	defer func() { _ = os.Remove(archive) }()
	if err := download(ctx, url, want, archive, e.Progress); err != nil {
		return fmt.Errorf("downloading the model: %w", err)
	}
	// Unpacked aside, so a failure doesn't look like a finished install.
	unpacking := e.modelUnpacked() + ".part"
	defer func() { _ = os.RemoveAll(unpacking) }()
	if err := os.RemoveAll(unpacking); err != nil {
		return err
	}
	if err := unpack(archive, unpacking); err != nil {
		return fmt.Errorf("unpacking the model: %w", err)
	}
	return os.Rename(filepath.Join(unpacking, "mlx-audio"), e.modelUnpacked())
}

// progressWriter reports each new percentage of total as bytes pass through.
type progressWriter struct {
	done, total int64
	last        int
	report      func(percent int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if percent := int(p.done * 100 / p.total); percent != p.last {
		p.last = percent
		p.report(percent)
	}
	return len(b), nil
}

// unpack extracts a .tar.gz of folders and regular files into dir.
func unpack(archive, dir string) error {
	f, err := os.Open(archive) // #nosec G304 -- inside acmd's folder
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("%q is outside the archive", h.Name)
		}
		path := filepath.Join(dir, h.Name) // #nosec G305 -- checked by IsLocal
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(path, tr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q isn't a file or folder", h.Name)
		}
	}
}

func writeFile(path string, from io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- checked by IsLocal
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, from) // #nosec G110 -- the archive's checksum was verified
	return err
}
