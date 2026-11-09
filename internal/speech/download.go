package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// retryWaits are the pauses before each retry of a download. GitHub sometimes
// answers a big download with a 502 or 504, or drops it partway, so a failed
// attempt continues from where the last one stopped.
var retryWaits = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}

// downloadClient gives up on a server that doesn't answer, rather than waiting forever.
var downloadClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second,
}}

// errFinal is a failure retrying won't fix, such as a missing file.
type errFinal struct{ error }

// download writes url to file, retrying and resuming on failures that can pass,
// then checks the whole file's SHA-256.
func download(ctx context.Context, url, want, file string, progress func(percent int)) error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = fetch(ctx, url, file, progress); err == nil {
			break
		}
		var final errFinal
		if errors.As(err, &final) || ctx.Err() != nil || attempt == len(retryWaits) {
			if errors.As(err, &final) {
				err = final.error
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryWaits[attempt]):
		}
	}
	got, err := fileSHA256(file)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the checksum doesn't match: got %s, want %s", got, want)
	}
	return nil
}

// fetch makes one attempt, continuing whatever is already in file.
func fetch(ctx context.Context, url, file string, progress func(percent int)) error {
	have := int64(0)
	if st, err := os.Stat(file); err == nil {
		have = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return errFinal{err}
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := downloadClient.Do(req) // #nosec G107 -- our own URL
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // the server sent it all again
		have = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		return nil // already complete; the checksum decides
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusRequestTimeout:
		return fmt.Errorf("%s: %s", url, resp.Status)
	default:
		return errFinal{fmt.Errorf("%s: %s", url, resp.Status)}
	}
	f, err := os.OpenFile(file, flags, 0o600) // #nosec G304 -- inside acmd's folder
	if err != nil {
		return errFinal{err}
	}
	var to io.Writer = f
	if progress != nil && resp.ContentLength > 0 {
		to = io.MultiWriter(f, &progressWriter{done: have, total: have + resp.ContentLength, report: progress, last: -1})
	}
	_, err = io.Copy(to, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file) // #nosec G304 -- inside acmd's folder
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
