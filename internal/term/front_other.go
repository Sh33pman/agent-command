//go:build !darwin && !linux

package term

import "os"

func front(*os.File, int) (Front, bool) { return Front{}, false }
