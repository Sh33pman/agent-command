package speech

import (
	"regexp"
	"strings"
)

// Prepare turns a Claude response (Markdown) into text to read aloud,
// split into chunks of at most about limit characters at sentence or
// paragraph boundaries (each chunk is synthesized while the previous one
// plays). Code blocks and tables aren't read: they become a short note.
func Prepare(markdown string, limit int) []string {
	text := spoken(markdown)
	var chunks []string
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.Join(strings.Fields(para), " ")
		if para == "" {
			continue
		}
		for len(para) > limit {
			cut := lastSentenceEnd(para[:limit])
			if cut <= 0 {
				if cut = strings.LastIndexByte(para[:limit], ' '); cut <= 0 {
					cut = limit
				}
			}
			chunks = append(chunks, strings.TrimSpace(para[:cut]))
			para = strings.TrimSpace(para[cut:])
		}
		if para != "" {
			chunks = append(chunks, para)
		}
	}
	return chunks
}

func lastSentenceEnd(s string) int {
	best := -1
	for _, end := range []string{". ", "! ", "? ", ": ", "; "} {
		if i := strings.LastIndex(s, end); i+1 > best {
			best = i + 1
		}
	}
	return best
}

var (
	linkRe     = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	urlRe      = regexp.MustCompile(`https?://\S+`)
	emphasisRe = regexp.MustCompile(`(\*\*|__|\*|_|~~)(\S(?:.*?\S)?)(\*\*|__|\*|_|~~)`)
	listRe     = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	headingRe  = regexp.MustCompile(`^\s*#{1,6}\s+`)
)

// Notes read in place of what isn't read aloud.
const (
	codeNote  = "There's a code block here; see the text."
	tableNote = "There's a table here; see the text."
)

// spoken strips Markdown down to what reads well aloud; paragraphs stay
// separated by blank lines.
func spoken(md string) string {
	var out []string
	inCode, inTable := false, false
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if !inCode {
				out = append(out, "", codeNote, "")
			}
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if strings.HasPrefix(t, "|") {
			if !inTable {
				out = append(out, "", tableNote, "")
			}
			inTable = true
			continue
		}
		inTable = false
		if t == "" || strings.Trim(t, "-*_= ") == "" {
			out = append(out, "")
			continue
		}
		t = headingRe.ReplaceAllString(t, "")
		heading := t != strings.TrimSpace(line) && headingRe.MatchString(strings.TrimSpace(line))
		t = listRe.ReplaceAllString(t, "")
		t = strings.TrimPrefix(t, "> ")
		t = linkRe.ReplaceAllString(t, "$1")
		t = urlRe.ReplaceAllString(t, "a link")
		t = strings.ReplaceAll(t, "`", "")
		t = emphasisRe.ReplaceAllString(t, "$2")
		if heading || listRe.MatchString(strings.TrimSpace(line)) {
			// Headings and list items are sentences of their own.
			if !strings.ContainsAny(t[len(t)-1:], ".!?:") {
				t += "."
			}
			out = append(out, "", t, "")
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}
