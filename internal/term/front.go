package term

import "path/filepath"

// interpreters run an agent written as a script; the script names it.
var interpreters = map[string]bool{"node": true, "bun": true, "deno": true, "python": true, "python3": true}

func programName(argv []string) string {
	name := filepath.Base(argv[0])
	if interpreters[name] {
		for _, a := range argv[1:] {
			if a != "" && a[0] != '-' {
				return filepath.Base(a)
			}
		}
	}
	return name
}
