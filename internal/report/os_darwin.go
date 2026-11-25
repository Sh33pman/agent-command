package report

import (
	"context"
	"os/exec"
	"strings"
)

func osName(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return "macOS " + v
	}
	return "macOS"
}
