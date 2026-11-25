package report

import (
	"context"
	"os"
)

func osName(context.Context) string {
	for _, f := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if b, err := os.ReadFile(f); err == nil { // #nosec G304 -- one of two fixed paths
			if name := osRelease(b); name != "" {
				return name
			}
		}
	}
	return "Linux"
}
