// Command check-ghostty says when Ghostty needs a look. Dependabot can't, as
// it's pinned in a shell script.
//
//	go run ./scripts/check-ghostty [--issue]
//
// It reports a release newer than scripts/build-ghostty.sh pins, and any
// security advisory not listed in .github/ghostty-advisories-reviewed.txt.
// With --issue, it opens an issue for them in $GITHUB_REPOSITORY, or updates
// the one already open; that needs $GH_TOKEN. It only reads Ghostty's public
// data.
//
// To clear an advisory, check it against the pinned commit, then add its id to
// that file.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	ghostty  = "ghostty-org/ghostty"
	reviewed = ".github/ghostty-advisories-reviewed.txt"
	title    = "Ghostty needs a security review"
)

// Advisory is the part of a GitHub security advisory we report.
type Advisory struct {
	ID       string `json:"ghsa_id"`
	URL      string `json:"html_url"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, newAPI()); err != nil {
		fmt.Fprintln(os.Stderr, "check-ghostty:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer, gh *api) error {
	script, err := os.ReadFile("scripts/build-ghostty.sh")
	if err != nil {
		return err
	}
	tag, commit, err := pinned(string(script))
	if err != nil {
		return err
	}
	text, err := os.ReadFile(reviewed)
	if err != nil {
		return err
	}
	done := reviewedIDs(string(text))

	var tags []struct {
		Name string `json:"name"`
	}
	if err := gh.all("repos/"+ghostty+"/tags", &tags); err != nil {
		return err
	}
	var advisories []Advisory
	if err := gh.all("repos/"+ghostty+"/security-advisories", &advisories); err != nil {
		return err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	releases, err := newerReleases(names, tag)
	if err != nil {
		return err
	}
	body := issueBody(tag, commit, releases, unreviewed(advisories, done))
	fmt.Fprintf(out, "Ghostty %s: %d tags, %d advisories, %d reviewed\n", tag, len(tags), len(advisories), len(done))
	if body == "" {
		fmt.Fprintln(out, "nothing new")
		return nil
	}
	fmt.Fprintln(out, body)
	if slices.Contains(args, "--issue") {
		return fileIssue(out, gh, os.Getenv("GITHUB_REPOSITORY"), body)
	}
	return nil
}

var (
	tagLine    = regexp.MustCompile(`(?m)^TAG=(\S+)`)
	commitLine = regexp.MustCompile(`(?m)^COMMIT=(\S+)`)
	release    = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
)

// pinned is the tag and commit the build script pins.
func pinned(script string) (tag, commit string, err error) {
	t, c := tagLine.FindStringSubmatch(script), commitLine.FindStringSubmatch(script)
	if t == nil || c == nil {
		return "", "", errors.New("no TAG or COMMIT in the build script")
	}
	return t[1], c[1], nil
}

// version is a release's numbers, or nil for anything else, such as tip or
// vt-1.0.0.
func version(tag string) []int {
	m := release.FindStringSubmatch(tag)
	if m == nil {
		return nil
	}
	v := make([]int, 3)
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v
}

// newerReleases are the releases after pin, newest first.
func newerReleases(tags []string, pin string) ([]string, error) {
	current := version(pin)
	if current == nil {
		return nil, fmt.Errorf("the pinned tag %s isn't a version", pin)
	}
	var found []string
	for _, t := range tags {
		if v := version(t); v != nil && slices.Compare(v, current) > 0 {
			found = append(found, t)
		}
	}
	slices.SortFunc(found, func(a, b string) int { return slices.Compare(version(b), version(a)) })
	return found, nil
}

// reviewedIDs are the advisory ids in the reviewed file, without comments.
func reviewedIDs(text string) map[string]bool {
	ids := map[string]bool{}
	for line := range strings.Lines(text) {
		id, _, _ := strings.Cut(line, "#")
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func unreviewed(advisories []Advisory, done map[string]bool) []Advisory {
	var out []Advisory
	for _, a := range advisories {
		if !done[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// issueBody is the text of an issue, or "" if there's nothing to look at.
func issueBody(tag, commit string, releases []string, advisories []Advisory) string {
	if len(releases) == 0 && len(advisories) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "We pin Ghostty %s (%s) in `scripts/build-ghostty.sh`.\n\n", tag, commit)
	if len(advisories) > 0 {
		b.WriteString("**Advisories not reviewed yet:**\n\n")
		for _, a := range advisories {
			fmt.Fprintf(&b, "- [%s](%s) (%s): %s\n", a.ID, a.URL, a.Severity, a.Summary)
		}
		b.WriteString("\nCheck each against the pinned commit, as being on a version isn't always enough: " +
			"some are fixed at a commit. Then add its id to `" + reviewed + "`.\n\n")
	}
	if len(releases) > 0 {
		fmt.Fprintf(&b, "**Newer releases:** %s. Moving to one means updating `TAG` and `COMMIT`.\n\n", strings.Join(releases, ", "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// fileIssue opens the issue, or updates the open one when the body changed.
func fileIssue(out io.Writer, gh *api, repo, body string) error {
	if repo == "" {
		return errors.New("--issue needs GITHUB_REPOSITORY (owner/name)")
	}
	var open []struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := gh.all("repos/"+repo+"/issues?state=open", &open); err != nil {
		return err
	}
	for _, i := range open {
		if i.Title != title || i.PullRequest != nil {
			continue
		}
		if i.Body == body {
			fmt.Fprintln(out, "already reported in issue", i.Number)
			return nil
		}
		issue := fmt.Sprintf("repos/%s/issues/%d", repo, i.Number)
		if err := gh.send(http.MethodPatch, issue, map[string]string{"body": body}, nil); err != nil {
			return err
		}
		note := map[string]string{"body": "There's something new, so the description above has changed."}
		if err := gh.send(http.MethodPost, issue+"/comments", note, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "updated issue", i.Number)
		return nil
	}
	var made struct {
		URL string `json:"html_url"`
	}
	if err := gh.send(http.MethodPost, "repos/"+repo+"/issues", map[string]string{"title": title, "body": body}, &made); err != nil {
		return err
	}
	fmt.Fprintln(out, made.URL)
	return nil
}

// api calls GitHub's REST API, with $GH_TOKEN when it's set.
type api struct {
	base   string
	token  string
	client *http.Client
}

func newAPI() *api {
	return &api{base: "https://api.github.com/", token: os.Getenv("GH_TOKEN"), client: &http.Client{Timeout: 30 * time.Second}}
}

// all fetches every page of a list into out, a pointer to a slice.
func (a *api) all(path string, out any) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var items []json.RawMessage
	for page := 1; ; page++ {
		var got []json.RawMessage
		if err := a.send(http.MethodGet, fmt.Sprintf("%s%sper_page=100&page=%d", path, sep, page), nil, &got); err != nil {
			return err
		}
		items = append(items, got...)
		if len(got) < 100 {
			break
		}
	}
	all, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(all, out)
}

func (a *api) send(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, body) //nolint:gosec // G704: base is GitHub's API (a test server in tests); path is ours
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req) //nolint:gosec // G704: as above
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s", method, strings.SplitN(path, "?", 2)[0], resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
