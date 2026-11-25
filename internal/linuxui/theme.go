//go:build linux

package linuxui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/brettmostert/agent-command/internal/paths"
)

// DefaultThemeLabel is the display label for the built-in default theme.
const DefaultThemeLabel = "Ghostty default"

// ThemeFolders returns the directories searched for Ghostty theme files.
func ThemeFolders() []string {
	var dirs []string
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		dirs = append(dirs, filepath.Join(cfg, "ghostty", "themes"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		dirs = append(dirs,
			filepath.Join(exeDir, "..", "share", "agent-command", "themes"),
			filepath.Join(exeDir, "ghostty-resources", "themes"),
			filepath.Join(exeDir, "..", "build", "ghostty", "ghostty-resources", "themes"),
		)
	}
	dirs = append(dirs,
		"/usr/share/ghostty/themes",
		"/usr/local/share/ghostty/themes",
		filepath.Join("build", "ghostty", "ghostty-resources", "themes"),
	)
	return dirs
}

// DiscoverThemes returns sorted unique theme names found across folders.
func DiscoverThemes(folders []string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, dir := range folders {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || e.IsDir() {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

// GhosttyConfigPath returns ~/.config/agent-command/ghostty.
func GhosttyConfigPath() (string, error) {
	p, err := paths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.ConfigDir, "ghostty"), nil
}

// ConfigValue parses the active "<key> = ..." value (last line wins, "" means none).
func ConfigValue(text, key string) string {
	var found string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		found = v
	}
	return found
}

// ThemeInConfig parses the active "theme = ..." value (last line wins, "" means none).
func ThemeInConfig(text string) string {
	return ConfigValue(text, "theme")
}

// SetThemeInConfig updates or removes the "theme = ..." line while preserving other lines.
func SetThemeInConfig(text, theme string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	placed := false
	var out []string
	for _, line := range lines {
		k, _, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "theme" {
			if placed {
				continue
			}
			placed = true
			if theme != "" {
				out = append(out, "theme = "+theme)
			}
			continue
		}
		out = append(out, line)
	}
	if !placed && theme != "" {
		out = append(out, "theme = "+theme)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func readGhosttyConfigs() []string {
	var out []string
	if p, err := GhosttyConfigPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil { // #nosec G304 -- user ghostty config path
			out = append(out, string(b))
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		if b, err := os.ReadFile(filepath.Join(cfg, "ghostty", "config")); err == nil { // #nosec G304 -- user ghostty config path
			out = append(out, string(b))
		}
	}
	return out
}

// ReadSavedTheme reads the saved theme name from ~/.config/agent-command/ghostty
// (falling back to ~/.config/ghostty/config if unset).
func ReadSavedTheme() string {
	for _, text := range readGhosttyConfigs() {
		if name := ThemeInConfig(text); name != "" {
			return name
		}
	}
	return ""
}

// ReadSavedFont reads font-family and font-size from ~/.config/agent-command/ghostty
// (falling back to ~/.config/ghostty/config).
func ReadSavedFont() (string, int) {
	var family string
	var size int
	for _, text := range readGhosttyConfigs() {
		if family == "" {
			family = ConfigValue(text, "font-family")
		}
		if size == 0 {
			if v := ConfigValue(text, "font-size"); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					size = n
				}
			}
		}
	}
	return family, size
}

// SaveTheme persists the selected theme name (or clears it when empty) in ~/.config/agent-command/ghostty.
func SaveTheme(name string) error {
	p, err := GhosttyConfigPath()
	if err != nil {
		return err
	}
	var existing string
	if b, err := os.ReadFile(p); err == nil { // #nosec G304 -- user ghostty config path
		existing = string(b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	next := SetThemeInConfig(existing, name)
	return os.WriteFile(p, []byte(next), 0o600)
}

// LoadTheme loads the named Ghostty theme from folders, falling back to DefaultPalette.
func LoadTheme(name string, folders []string) Palette {
	if strings.TrimSpace(name) == "" {
		return DefaultPalette
	}
	for _, dir := range folders {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p) // #nosec G304 -- theme file inside discovered theme folder
		if err == nil {
			return ParseGhosttyTheme(string(b))
		}
	}
	return DefaultPalette
}

func mixRGB(fg, bg RGB, amount float32) RGB {
	return RGB{
		R: fg.R*(1-amount) + bg.R*amount,
		G: fg.G*(1-amount) + bg.G*amount,
		B: fg.B*(1-amount) + bg.B*amount,
	}
}

func cssHex(c RGB) string {
	clamp := func(v float32) int {
		n := int(v*255 + 0.5)
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(c.R), clamp(c.G), clamp(c.B))
}

// ThemeCSS builds GTK4 CSS styling the window, sidebar, headerbars, text, and
// Ghostty font in the active palette's colors (matching AppTheme.swift on macOS).
func ThemeCSS(p Palette, fontFamily string, fontSize int) string {
	bg := cssHex(p.Background)
	fg := cssHex(p.Foreground)
	dim := cssHex(mixRGB(p.Foreground, p.Background, 0.45))
	line := cssHex(mixRGB(p.Foreground, p.Background, 0.82))
	red := cssHex(p.Colors[1])
	green := cssHex(p.Colors[2])
	yellow := cssHex(p.Colors[3])
	blue := cssHex(p.Colors[4])
	magenta := cssHex(p.Colors[5])
	cyan := cssHex(p.Colors[6])

	var fontRules strings.Builder
	if fontFamily != "" {
		safe := strings.ReplaceAll(fontFamily, `"`, `\"`)
		fmt.Fprintf(&fontRules, "\tfont-family: \"%s\", monospace;\n", safe)
	} else {
		fontRules.WriteString("\tfont-family: monospace;\n")
	}
	if fontSize > 0 {
		fmt.Fprintf(&fontRules, "\tfont-size: %dpt;\n", fontSize)
	}

	return fmt.Sprintf(`
window, .background, .sidebar-pane, .content-pane, popover > contents, dialog {
	background-color: %s;
	color: %s;
}
.sidebar-pane {
	border-right: 1px solid %s;
}
headerbar {
	background-color: %s;
	color: %s;
	border-bottom: 1px solid %s;
	box-shadow: none;
}
.navigation-sidebar, list.navigation-sidebar {
	background-color: %s;
	color: %s;
}
.navigation-sidebar, .navigation-sidebar label, headerbar, dialog, .caption {
%s}
.navigation-sidebar row {
	color: %s;
	border-radius: 6px;
	margin: 1px 6px;
}
.navigation-sidebar row:selected,
.navigation-sidebar row:selected:focus {
	background-color: %s;
	color: %s;
}
.navigation-sidebar row:selected label,
.navigation-sidebar row:selected .dim-label {
	color: %s;
}
.dim-label {
	color: %s;
}
.accent, .status-waiting {
	color: %s;
}
.status-error {
	color: %s;
}
.group-red { color: %s; }
.group-green { color: %s; }
.group-yellow { color: %s; }
.group-blue { color: %s; }
.group-magenta { color: %s; }
.group-cyan { color: %s; }
.suggested-action {
	background-color: %s;
	color: %s;
}
searchbar > revealer > box {
	background-color: %s;
	border-bottom: 1px solid %s;
}
`, bg, fg, line, bg, fg, line, bg, fg, fontRules.String(), fg, yellow, bg, bg, dim, blue, red, red, green, yellow, blue, magenta, cyan, yellow, bg, bg, line)
}
