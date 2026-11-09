package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyServer serves data, answering each request with the next of steps:
// a status to fail with, "drop" to send half and cut the connection, or "ok".
// It honors Range unless ignoreRange.
type flakyServer struct {
	data        []byte
	ignoreRange bool
	mu          sync.Mutex
	steps       []string
	ranges      []string
}

func (s *flakyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	step := "ok"
	if len(s.steps) > 0 {
		step, s.steps = s.steps[0], s.steps[1:]
	}
	s.ranges = append(s.ranges, r.Header.Get("Range"))
	s.mu.Unlock()
	if code, err := strconv.Atoi(step); err == nil {
		w.WriteHeader(code)
		return
	}
	body, status := s.data, http.StatusOK
	if from, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes="); ok && !s.ignoreRange {
		n, _ := strconv.Atoi(strings.TrimSuffix(from, "-"))
		body, status = s.data[n:], http.StatusPartialContent
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if step == "drop" {
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	_, _ = w.Write(body)
}

func quickRetries(t *testing.T) {
	old := retryWaits
	retryWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryWaits = old })
}

func runDownload(t *testing.T, s *flakyServer) (string, error) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	h := sha256.Sum256(s.data)
	file := filepath.Join(t.TempDir(), "model.part")
	return file, download(context.Background(), srv.URL, hex.EncodeToString(h[:]), file, nil)
}

func testData() []byte { return []byte(strings.Repeat("model bytes ", 5000)) }

func TestDownloadRetriesAGatewayTimeout(t *testing.T) {
	quickRetries(t)
	s := &flakyServer{data: testData(), steps: []string{"504", "502"}}
	file, err := runDownload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != string(s.data) {
		t.Error("wrong content")
	}
}

func TestDownloadResumesWhereItStopped(t *testing.T) {
	quickRetries(t)
	s := &flakyServer{data: testData(), steps: []string{"drop"}}
	file, err := runDownload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != string(s.data) {
		t.Error("wrong content")
	}
	if len(s.ranges) != 2 || s.ranges[1] != "bytes="+strconv.Itoa(len(s.data)/2)+"-" {
		t.Errorf("ranges asked for: %q", s.ranges)
	}
}

func TestDownloadStartsAgainWhenTheServerIgnoresRange(t *testing.T) {
	quickRetries(t)
	s := &flakyServer{data: testData(), steps: []string{"drop"}, ignoreRange: true}
	file, err := runDownload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != string(s.data) {
		t.Error("wrong content")
	}
}

func TestDownloadDoesntRetryAMissingFile(t *testing.T) {
	quickRetries(t)
	s := &flakyServer{data: testData(), steps: []string{"404"}}
	if _, err := runDownload(t, s); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	if len(s.ranges) != 1 {
		t.Errorf("asked %d times for a missing file", len(s.ranges))
	}
}

func TestDownloadGivesUpAfterItsRetries(t *testing.T) {
	quickRetries(t)
	s := &flakyServer{data: testData(), steps: []string{"504", "504", "504", "504", "504"}}
	if _, err := runDownload(t, s); err == nil || !strings.Contains(err.Error(), "504") {
		t.Fatalf("err = %v", err)
	}
	if len(s.ranges) != len(retryWaits)+1 {
		t.Errorf("asked %d times, want %d", len(s.ranges), len(retryWaits)+1)
	}
}
