package cli

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestReportPageCleansAndNeverPosts(t *testing.T) {
	cmd := newReportPageCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("It broke, with sk-ant-api03-abcdefghijklmnopqrstuvwxyz."))
	cmd.SetArgs([]string{"--kind", "bug", "--title", "A title"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got reportPage
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.PageURL, "https://github.com/brettmostert/homebrew-tap/issues/new?") {
		t.Errorf("page %s", got.PageURL)
	}
	page, err := url.QueryUnescape(got.PageURL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, "sk-ant") || strings.Contains(got.Body, "sk-ant") {
		t.Errorf("a secret reached the report: %+v", got)
	}
	if got.Cut || !strings.Contains(page, "A title") || !strings.Contains(page, "labels=bug") {
		t.Errorf("result %+v", got)
	}
}

func TestReportPageNeedsATitle(t *testing.T) {
	cmd := newReportPageCmd()
	cmd.SetIn(strings.NewReader("body"))
	cmd.SetArgs([]string{"--title", "  "})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Error("a report without a title should fail")
	}
}
