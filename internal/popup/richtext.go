package popup

import (
	"regexp"
	"strings"
)

// Block kinds.
const (
	BlockPara   = "p"      // a paragraph or heading line
	BlockBullet = "bullet" // a list item; Marker is "•" or "1."
	BlockCode   = "code"   // a fenced code block (one plain span)
	BlockBlank  = "blank"  // vertical space
)

// Span styles.
const (
	SpanText = ""
	SpanCode = "code"
	SpanLink = "link"
	SpanBold = "bold"
)

// Span is a run of text with one style. URL is set for links.
type Span struct {
	Text  string `json:"text"`
	Style string `json:"style,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Block is one line-level element of a response. The panel draws Blocks;
// Body stays the plain text (speech, copy).
type Block struct {
	Kind   string `json:"kind"`
	Indent int    `json:"indent,omitempty"` // list nesting level
	Marker string `json:"marker,omitempty"` // bullets: "•", "1."
	Head   int    `json:"head,omitempty"`   // paragraph: heading level (1-6), 0 = body
	Spans  []Span `json:"spans,omitempty"`
}

var (
	fenceRe  = regexp.MustCompile("^\\s*(```|~~~)")
	bulletRe = regexp.MustCompile(`^(\s*)([-*+]|\d{1,3}[.)])\s+(.*)$`)
	headRe   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	// Inline: `code`, [text](url), **bold**, or a bare http(s) URL.
	inlineRe = regexp.MustCompile("`([^`\n]+)`|\\[([^\\]\n]+)\\]\\((https?://[^\\s)]+)\\)|\\*\\*([^*\n]+)\\*\\*|(https?://[^\\s<>\"`]+)")
)

// ParseRich turns a response written in light markdown into Blocks:
// fenced code blocks, bullet and numbered lists, headings, and inline
// code, bold, links and bare URLs. Anything else is plain text, so a
// response without markdown comes out as paragraphs line by line.
func ParseRich(body string) []Block {
	var blocks []Block
	var code []string
	inCode := false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if fenceRe.MatchString(line) {
			if inCode {
				blocks = append(blocks, Block{Kind: BlockCode, Spans: []Span{{Text: strings.Join(code, "\n"), Style: SpanCode}}})
				code, inCode = nil, false
			} else {
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		switch {
		case strings.TrimSpace(line) == "":
			blocks = append(blocks, Block{Kind: BlockBlank})
		case bulletRe.MatchString(line):
			m := bulletRe.FindStringSubmatch(line)
			marker := "•"
			if m[2][0] >= '0' && m[2][0] <= '9' {
				marker = m[2]
			}
			blocks = append(blocks, Block{Kind: BlockBullet, Indent: indentLevel(m[1]), Marker: marker, Spans: parseInline(m[3])})
		case headRe.MatchString(line):
			m := headRe.FindStringSubmatch(line)
			blocks = append(blocks, Block{Kind: BlockPara, Head: len(m[1]), Spans: parseInline(m[2])})
		default:
			blocks = append(blocks, Block{Kind: BlockPara, Spans: parseInline(line)})
		}
	}
	if inCode { // an unclosed fence: show what there is
		blocks = append(blocks, Block{Kind: BlockCode, Spans: []Span{{Text: strings.Join(code, "\n"), Style: SpanCode}}})
	}
	return blocks
}

// indentLevel counts nesting from leading whitespace (2 spaces or a tab per level).
func indentLevel(ws string) int {
	n := 0
	for _, r := range ws {
		if r == '\t' {
			n += 2
		} else {
			n++
		}
	}
	return n / 2
}

func parseInline(s string) []Span {
	var spans []Span
	last := 0
	add := func(sp Span) {
		if sp.Text == "" {
			return
		}
		if n := len(spans); n > 0 && sp.Style == SpanText && spans[n-1].Style == SpanText {
			spans[n-1].Text += sp.Text // e.g. the punctuation after a bare URL
			return
		}
		spans = append(spans, sp)
	}
	for _, m := range inlineRe.FindAllStringSubmatchIndex(s, -1) {
		add(Span{Text: s[last:m[0]]})
		switch {
		case m[2] >= 0: // `code`
			add(Span{Text: s[m[2]:m[3]], Style: SpanCode})
		case m[4] >= 0: // [text](url)
			add(Span{Text: s[m[4]:m[5]], Style: SpanLink, URL: s[m[6]:m[7]]})
		case m[8] >= 0: // **bold**
			add(Span{Text: s[m[8]:m[9]], Style: SpanBold})
		default: // bare URL: trailing punctuation belongs to the sentence
			u := s[m[10]:m[11]]
			trimmed := strings.TrimRight(u, ".,;:!?)]}'")
			add(Span{Text: trimmed, Style: SpanLink, URL: trimmed})
			add(Span{Text: u[len(trimmed):]})
		}
		last = m[1]
	}
	add(Span{Text: s[last:]})
	return spans
}
