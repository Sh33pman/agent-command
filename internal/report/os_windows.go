package report

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"
)

func osName(context.Context) string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
