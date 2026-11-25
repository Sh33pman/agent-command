//go:build !darwin && !linux && !windows

package report

import (
	"context"
	"runtime"
)

func osName(context.Context) string { return runtime.GOOS }
