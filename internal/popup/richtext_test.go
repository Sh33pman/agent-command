package popup

import (
	"reflect"
	"testing"
)

func TestParseRichInline(t *testing.T) {
	got := parseInline("Run `go test` and see https://example.com/a?b=1, or [docs](https://x.dev/d). **Done**")
	want := []Span{
		{Text: "Run "},
		{Text: "go test", Style: SpanCode},
		{Text: " and see "},
		{Text: "https://example.com/a?b=1", Style: SpanLink, URL: "https://example.com/a?b=1"},
		{Text: ", or "},
		{Text: "docs", Style: SpanLink, URL: "https://x.dev/d"},
		{Text: ". "},
		{Text: "Done", Style: SpanBold},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestParseRichBlocks(t *testing.T) {
	body := "Changes:\n- one\n  * nested `x`\n1. first\n\n```go\nfmt.Println(\"- not a bullet\")\n```\n# Title"
	got := ParseRich(body)
	kinds := []string{}
	for _, b := range got {
		kinds = append(kinds, b.Kind)
	}
	if want := []string{"p", "bullet", "bullet", "bullet", "blank", "code", "p"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	if got[2].Indent != 1 || got[2].Marker != "•" {
		t.Errorf("nested bullet: %+v", got[2])
	}
	if got[3].Marker != "1." {
		t.Errorf("numbered marker: %+v", got[3])
	}
	if c := got[5].Spans[0]; c.Text != `fmt.Println("- not a bullet")` || c.Style != SpanCode {
		t.Errorf("code block: %+v", c)
	}
	if got[6].Head != 1 {
		t.Errorf("heading: %+v", got[6])
	}
}

func TestParseRichUnclosedFenceAndPlain(t *testing.T) {
	got := ParseRich("```\nabc")
	if len(got) != 1 || got[0].Kind != BlockCode || got[0].Spans[0].Text != "abc" {
		t.Errorf("unclosed fence: %+v", got)
	}
	got = ParseRich("just text")
	if len(got) != 1 || got[0].Spans[0].Text != "just text" {
		t.Errorf("plain: %+v", got)
	}
}
