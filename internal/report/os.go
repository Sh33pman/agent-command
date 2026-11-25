package report

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
)

// OS is this machine's OS and its version, for a report, such as
// "macOS 27.2" or "Ubuntu 24.04.1 LTS".
func OS(ctx context.Context) string { return osName(ctx) }

// osRelease is the OS that an os-release file names: its PRETTY_NAME, or its
// NAME and VERSION_ID ("" if it names none).
func osRelease(file []byte) string {
	vals := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(file))
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		if s, err := strconv.Unquote(val); err == nil {
			val = s
		} else {
			val = strings.Trim(val, `"'`)
		}
		vals[key] = val
	}
	if p := vals["PRETTY_NAME"]; p != "" {
		return p
	}
	return strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"])
}
