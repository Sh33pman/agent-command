package linuxui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeConfigParsingAndUpdate(t *testing.T) {
	cfg := "# comment\nfont-size = 13\ntheme = TokyoNight\n"
	if got := ThemeInConfig(cfg); got != "TokyoNight" {
		t.Fatalf("ThemeInConfig = %q, want TokyoNight", got)
	}

	updated := SetThemeInConfig(cfg, "Duskfox")
	if got := ThemeInConfig(updated); got != "Duskfox" {
		t.Fatalf("updated ThemeInConfig = %q, want Duskfox", got)
	}
	if !strings.Contains(updated, "font-size = 13") {
		t.Fatal("expected other lines to be preserved")
	}

	cleared := SetThemeInConfig(updated, "")
	if got := ThemeInConfig(cleared); got != "" {
		t.Fatalf("cleared ThemeInConfig = %q, want empty", got)
	}
}

func TestDiscoverAndLoadTheme(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Duskfox"), []byte("background = #232136\nforeground = #e0def4\npalette = 4=#3e8fb0\n"), 0o644); err != nil {
		t.Fatalf("write theme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}

	names := DiscoverThemes([]string{dir})
	if len(names) != 1 || names[0] != "Duskfox" {
		t.Fatalf("DiscoverThemes = %v, want [Duskfox]", names)
	}

	p := LoadTheme("Duskfox", []string{dir})
	wantBG, _ := ParseHexColor("#232136")
	if p.Background != wantBG {
		t.Fatalf("loaded bg = %+v, want %+v", p.Background, wantBG)
	}

	css := ThemeCSS(p)
	if !strings.Contains(css, "#232136") || !strings.Contains(css, "#e0def4") {
		t.Fatalf("ThemeCSS missing theme colors: %s", css)
	}
}

func TestSaveAndReadSavedTheme(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	// Falls back to ~/.config/ghostty/config when agent-command/ghostty is absent.
	ghosttyDir := filepath.Join(cfgDir, "ghostty")
	if err := os.MkdirAll(ghosttyDir, 0o755); err != nil {
		t.Fatalf("mkdir ghostty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ghosttyDir, "config"), []byte("theme = \"Aura Dark\"\n"), 0o644); err != nil {
		t.Fatalf("write ghostty config: %v", err)
	}
	if got := ReadSavedTheme(); got != "Aura Dark" {
		t.Fatalf("fallback ReadSavedTheme = %q, want Aura Dark", got)
	}

	if err := SaveTheme("TokyoNight"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}
	if got := ReadSavedTheme(); got != "TokyoNight" {
		t.Fatalf("ReadSavedTheme = %q, want TokyoNight", got)
	}

	if len(ThemeFolders()) == 0 {
		t.Fatal("expected non-empty ThemeFolders")
	}
	if got := LoadTheme("", nil); got != DefaultPalette {
		t.Fatal("expected DefaultPalette for empty theme name")
	}
	if got := LoadTheme("nonexistent-theme", []string{cfgDir}); got != DefaultPalette {
		t.Fatal("expected DefaultPalette for missing theme")
	}
}
