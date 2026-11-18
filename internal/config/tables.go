package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// KeyValue is one key of a table AppendTable writes, in the order given.
type KeyValue struct {
	Key   string
	Value any
}

// AppendTable adds a [[array]] table with the keys given to the end of the
// TOML text, keeping everything else byte for byte. The result is checked to
// parse and to hold one more table.
func AppendTable(text, array string, kv ...KeyValue) (string, error) {
	before, err := countTables(text, array)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	if strings.TrimSpace(text) != "" {
		b.WriteString("\n")
	}
	b.WriteString("[[" + array + "]]\n")
	for _, p := range kv {
		v, err := renderValue(p.Value)
		if err != nil {
			return "", err
		}
		b.WriteString(p.Key + " = " + v + "\n")
	}
	result := b.String()
	after, err := countTables(result, array)
	if err != nil {
		return "", fmt.Errorf("config.toml: adding to %s: %w", array, err)
	}
	if after != before+1 {
		return "", fmt.Errorf("config.toml: %s has %d entries after adding one to %d", array, after, before)
	}
	return result, nil
}

// RemoveTables takes out the [[array]] tables that match, and says how many
// it took. A table runs from its header to the next header; comments and
// blank lines at its end stay, as they're more likely about what follows.
// Everything else is kept byte for byte, and the result is checked to parse.
func RemoveTables(text, array string, match func(table map[string]any) bool) (string, int, error) {
	if _, err := countTables(text, array); err != nil {
		return "", 0, fmt.Errorf("config.toml: %w", err)
	}
	header := regexp.MustCompile(`^\s*\[\[\s*` + regexp.QuoteMeta(array) + `\s*\]\]\s*(#.*)?$`)
	lines := strings.SplitAfter(text, "\n")
	isHeader := func(l string) bool {
		t := strings.TrimSpace(l)
		return sectionRe.MatchString(strings.TrimRight(l, "\r\n")) || strings.HasPrefix(t, "[[")
	}
	var out []string
	removed := 0
	for i := 0; i < len(lines); {
		if !header.MatchString(strings.TrimRight(lines[i], "\r\n")) {
			out = append(out, lines[i])
			i++
			continue
		}
		end := i + 1
		for end < len(lines) && !isHeader(lines[end]) {
			end++
		}
		// What trails the table's last key belongs to what follows.
		last := end
		for last > i+1 {
			t := strings.TrimSpace(lines[last-1])
			if t != "" && !strings.HasPrefix(t, "#") {
				break
			}
			last--
		}
		var table map[string]any
		if _, err := toml.Decode(strings.Join(lines[i+1:last], ""), &table); err != nil {
			return "", 0, fmt.Errorf("config.toml: reading %s: %w", array, err)
		}
		if !match(table) {
			out = append(out, lines[i:end]...)
			i = end
			continue
		}
		removed++
		// One blank line between what's left is enough.
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, lines[last:end]...)
		i = end
	}
	if removed == 0 {
		return text, 0, nil
	}
	result := strings.Join(out, "")
	var doc map[string]any
	if _, err := toml.Decode(result, &doc); err != nil {
		return "", 0, fmt.Errorf("config.toml: removing from %s: %w", array, err)
	}
	return result, removed, nil
}

// countTables is how many tables the array (a dotted path) holds in text.
func countTables(text, array string) (int, error) {
	var doc map[string]any
	if _, err := toml.Decode(text, &doc); err != nil {
		return 0, err
	}
	keys := strings.Split(array, ".")
	var v any = doc
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return 0, nil
		}
		v = m[k]
	}
	switch v := v.(type) {
	case nil:
		return 0, nil
	case []map[string]any:
		return len(v), nil
	default:
		return 0, fmt.Errorf("%s isn't an array of tables", array)
	}
}
