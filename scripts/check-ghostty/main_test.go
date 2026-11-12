package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestReadsThePin(t *testing.T) {
	tag, commit, err := pinned("set -e\nTAG=v1.4.0\nCOMMIT=83fb\n")
	if tag != "v1.4.0" || commit != "83fb" || err != nil {
		t.Errorf("%q %q %v", tag, commit, err)
	}
	if _, _, err := pinned("nothing here"); err == nil {
		t.Error("no pin")
	}
}

func TestOnlyRealNewerVersionsCount(t *testing.T) {
	tags := []string{"tip", "vt-1.0.0", "v1.4.0", "v1.3.1", "v1.4.1", "v1.10.0", "v2.0.0-rc1"}
	if got, _ := newerReleases(tags, "v1.4.0"); !reflect.DeepEqual(got, []string{"v1.10.0", "v1.4.1"}) {
		t.Error(got)
	}
	if got, _ := newerReleases([]string{"v1.4.0", "v1.3.1"}, "v1.4.0"); len(got) != 0 {
		t.Error(got)
	}
	if _, err := newerReleases(nil, "tip"); err == nil {
		t.Error("tip pinned")
	}
}

func TestReviewedListIgnoresCommentsAndBlanks(t *testing.T) {
	got := reviewedIDs("# header\n\nGHSA-1  # fine\nGHSA-2\n")
	if !reflect.DeepEqual(got, map[string]bool{"GHSA-1": true, "GHSA-2": true}) {
		t.Error(got)
	}
	if left := unreviewed([]Advisory{advisory("GHSA-1"), advisory("GHSA-2")}, got); len(left) != 0 {
		t.Error(left)
	}
	if got := unreviewed([]Advisory{advisory("GHSA-3")}, nil); len(got) != 1 {
		t.Error(got)
	}
}

func TestIssueBodySaysWhatToDo(t *testing.T) {
	if b := issueBody("v1.4.0", "83fb", nil, nil); b != "" {
		t.Errorf("nothing new: %q", b)
	}
	b := issueBody("v1.4.0", "83fb", []string{"v1.5.0"}, []Advisory{advisory("GHSA-9")})
	for _, part := range []string{"v1.4.0", "83fb", "v1.5.0", "GHSA-9", "https://x/GHSA-9", "high", reviewed} {
		if !strings.Contains(b, part) {
			t.Errorf("missing %q in %q", part, b)
		}
	}
	if strings.Contains(issueBody("v1.4.0", "83fb", []string{"v1.5.0"}, nil), "Advisories") {
		t.Error("advisories with none")
	}
	if strings.Contains(issueBody("v1.4.0", "83fb", nil, []Advisory{advisory("GHSA-9")}), "Newer") {
		t.Error("releases with none")
	}
}

func TestReviewedIDsLookRight(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", reviewed))
	if err != nil {
		t.Fatal(err)
	}
	ids := reviewedIDs(string(text))
	if len(ids) == 0 {
		t.Fatal("none")
	}
	ghsa := regexp.MustCompile(`^GHSA-[a-z0-9]{4}-[a-z0-9]{4}-[a-z0-9]{4}$`)
	for id := range ids {
		if !ghsa.MatchString(id) {
			t.Error(id)
		}
	}
}

// fakeGitHub serves the calls the command makes, so nothing reaches GitHub.
type fakeGitHub struct {
	mu     sync.Mutex
	calls  []string
	issues []map[string]any
	tags   int
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer token" {
		http.Error(w, "no token", http.StatusUnauthorized)
		return
	}
	page := r.URL.Query().Get("page")
	switch {
	case r.URL.Path == "/repos/ghostty-org/ghostty/tags":
		// Two pages: 100 old tags, then a newer release.
		var tags []map[string]string
		if page == "1" {
			for i := range 100 {
				tags = append(tags, map[string]string{"name": fmt.Sprintf("v1.0.%d", i)})
			}
		} else {
			tags = append(tags, map[string]string{"name": "v1.4.0"}, map[string]string{"name": "v9.0.0"})
		}
		f.tags += len(tags)
		_ = json.NewEncoder(w).Encode(tags)
	case r.URL.Path == "/repos/ghostty-org/ghostty/security-advisories":
		_ = json.NewEncoder(w).Encode([]Advisory{advisory("GHSA-new0-0000-0000")})
	case r.URL.Path == "/repos/me/repo/issues" && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.issues)
	case r.Method == http.MethodPost && r.URL.Path == "/repos/me/repo/issues":
		_, _ = io.WriteString(w, `{"html_url":"https://x/issues/1"}`)
	case r.URL.Path == "/repos/me/repo/issues/404":
		http.NotFound(w, r)
	default:
		_, _ = io.WriteString(w, "{}")
	}
}

func (f *fakeGitHub) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	return out
}

func serve(t *testing.T, f *fakeGitHub) *api {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &api{base: srv.URL + "/", token: "token", client: srv.Client()}
}

func TestRunReportsAndFilesAnIssue(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	t.Setenv("GITHUB_REPOSITORY", "me/repo")
	f := &fakeGitHub{}
	var out strings.Builder
	if err := run([]string{"--issue"}, &out, serve(t, f)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"102 tags", "GHSA-new0-0000-0000", "**Newer releases:** v9.0.0", "https://x/issues/1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if w := f.writes(); !reflect.DeepEqual(w, []string{"POST /repos/me/repo/issues"}) {
		t.Errorf("writes: %v", w)
	}

	// Without --issue it only reports.
	f = &fakeGitHub{}
	out.Reset()
	if err := run(nil, &out, serve(t, f)); err != nil || len(f.writes()) != 0 {
		t.Errorf("report only: %v %v", err, f.writes())
	}
}

func TestFileIssue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		issues []map[string]any
		want   []string
	}{
		{"opens one when there's none", nil, []string{"POST /repos/me/repo/issues"}},
		{"another title doesn't count", []map[string]any{{"number": 4, "title": "Other", "body": "x"}},
			[]string{"POST /repos/me/repo/issues"}},
		{"a pull request doesn't count", []map[string]any{{"number": 4, "title": title, "body": "x", "pull_request": map[string]any{}}},
			[]string{"POST /repos/me/repo/issues"}},
		{"leaves the same report alone", []map[string]any{{"number": 4, "title": title, "body": "the report"}}, nil},
		{"updates it when something is new", []map[string]any{{"number": 4, "title": title, "body": "older"}},
			[]string{"PATCH /repos/me/repo/issues/4", "POST /repos/me/repo/issues/4/comments"}},
	} {
		f := &fakeGitHub{issues: tc.issues}
		if err := fileIssue(io.Discard, serve(t, f), "me/repo", "the report"); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if w := f.writes(); !reflect.DeepEqual(w, tc.want) {
			t.Errorf("%s: %v", tc.name, w)
		}
	}
	if err := fileIssue(io.Discard, serve(t, &fakeGitHub{}), "", "x"); err == nil {
		t.Error("no repo")
	}
	gone := &fakeGitHub{issues: []map[string]any{{"number": 404, "title": title, "body": "older"}}}
	if err := fileIssue(io.Discard, serve(t, gone), "me/repo", "new"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("an error from GitHub: %v", err)
	}
	noToken := serve(t, &fakeGitHub{})
	noToken.token = ""
	if err := fileIssue(io.Discard, noToken, "me/repo", "x"); err == nil || strings.Contains(err.Error(), "token") {
		t.Errorf("unauthorized: %v", err)
	}
}

func advisory(id string) Advisory {
	return Advisory{ID: id, URL: "https://x/" + id, Severity: "high", Summary: "bad"}
}
