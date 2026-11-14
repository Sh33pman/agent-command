package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/speech"
)

// `acmd config`: the settings in config.toml, for people and for the app's
// Settings window. The editor is chosen with `acmd editor set`, which finds it.

// setting is one value `acmd config set` can change.
type setting struct {
	key   string // as shown: "notify.cooldown"
	parse func(string) (any, error)
	get   func(config.Config) any
}

func (s setting) section() (string, string) {
	if section, name, ok := strings.Cut(s.key, "."); ok {
		return section, name
	}
	return "", s.key
}

// Voices are Kokoro's American English voices, the ones its model can speak.
var Voices = []string{
	"af_heart", "af_alloy", "af_aoede", "af_bella", "af_jessica", "af_kore", "af_nicole", "af_nova", "af_river",
	"af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael", "am_onyx",
	"am_puck", "am_santa",
}

func boolSetting(key string, get func(config.Config) bool) setting {
	return setting{key: key, get: func(c config.Config) any { return get(c) }, parse: func(v string) (any, error) {
		switch strings.ToLower(v) {
		case "true", "on", "yes", "1":
			return true, nil
		case "false", "off", "no", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%s is on or off, not %q", key, v)
	}}
}

func durationSetting(key string, get func(config.Config) string) setting {
	return setting{key: key, get: func(c config.Config) any { return get(c) }, parse: func(v string) (any, error) {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("%s is a length of time like 10s or 2m, not %q", key, v)
		}
		return d.String(), nil
	}}
}

func choiceSetting(key string, choices []string, get func(config.Config) string) setting {
	return setting{key: key, get: func(c config.Config) any { return get(c) }, parse: func(v string) (any, error) {
		if !slices.Contains(choices, v) {
			return nil, fmt.Errorf("%s is one of %s, not %q", key, strings.Join(choices, ", "), v)
		}
		return v, nil
	}}
}

// settings are what can be set, in the order they're shown.
var settings = []setting{
	{key: "claude_command", get: func(c config.Config) any { return c.ClaudeCommand }, parse: func(v string) (any, error) {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("claude_command can't be empty")
		}
		return strings.TrimSpace(v), nil
	}},
	{key: "claude_args", get: func(c config.Config) any { return orEmpty(c.ClaudeArgs) }, parse: parseList},
	boolSetting("remote_control", func(c config.Config) bool { return c.RemoteControl }),
	boolSetting("notify.enabled", func(c config.Config) bool { return c.Notify.Enabled }),
	boolSetting("notify.on_waiting", func(c config.Config) bool { return c.Notify.OnWaiting }),
	boolSetting("notify.on_error", func(c config.Config) bool { return c.Notify.OnError }),
	boolSetting("notify.on_done", func(c config.Config) bool { return c.Notify.OnDone }),
	boolSetting("notify.on_paused", func(c config.Config) bool { return c.Notify.OnPaused }),
	durationSetting("notify.done_min_duration", func(c config.Config) string { return c.Notify.DoneMinDuration }),
	durationSetting("notify.cooldown", func(c config.Config) string { return c.Notify.Cooldown }),
	choiceSetting("notify.sound", []string{"default", ""}, func(c config.Config) string { return c.Notify.Sound }),
	{key: "speech.voice", get: func(c config.Config) any { return c.Speech.Voice }, parse: func(v string) (any, error) {
		v = strings.TrimSpace(v)
		if err := speech.CheckVoice(v); err != nil {
			return nil, err
		}
		return v, nil
	}},
	{key: "speech.speed", get: func(c config.Config) any { return c.Speech.Speed }, parse: func(v string) (any, error) {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0.5 || f > 2 {
			return nil, fmt.Errorf("speech.speed is from 0.5 to 2, not %q", v)
		}
		return f, nil
	}},
	choiceSetting("editor.run_in", []string{"background", "tab"}, func(c config.Config) string { return c.Editor.RunIn }),
	choiceSetting("editor.target", []string{"cwd", "git_root"}, func(c config.Config) string { return c.Editor.Target }),
}

// shownOnly are shown with the settings, but set elsewhere.
var shownOnly = []setting{
	{key: "editor.preset", get: func(c config.Config) any { return c.Editor.Preset }},
	{key: "editor.command", get: func(c config.Config) any { return orEmpty(c.Editor.Command) }},
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// parseList reads a JSON array, as the app sends, or words separated by spaces.
func parseList(v string) (any, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		var list []string
		if err := json.Unmarshal([]byte(v), &list); err != nil {
			return nil, fmt.Errorf("claude_args: %w", err)
		}
		return orEmpty(list), nil
	}
	return orEmpty(strings.Fields(v)), nil
}

func findSetting(key string) (setting, error) {
	for _, s := range settings {
		if s.key == key {
			return s, nil
		}
	}
	for _, s := range shownOnly {
		if s.key == key {
			return setting{}, fmt.Errorf("%s is set with: acmd editor set", key)
		}
	}
	return setting{}, fmt.Errorf("no setting %q (acmd config show lists them)", key)
}

// configView is what `acmd config show --json` prints.
type configView struct {
	Path     string         `json:"path"`
	Settings map[string]any `json:"settings"`
	Voices   []string       `json:"voices"`
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change acmd's settings (config.toml)",
		Long: "The settings in config.toml, which the app's Settings window (⌘,) also changes. A change to\n" +
			"notifications applies straight away; Claude's settings apply to sessions started after it,\n" +
			"and a new voice needs `acmd speech install`.",
		Args: cobra.NoArgs,
	}
	var asJSON bool
	show := &cobra.Command{
		Use:   "show",
		Short: "List the settings and their values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			cfg, err := config.Load(p.Config())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			all := append(append([]setting(nil), settings...), shownOnly...)
			if asJSON {
				view := configView{Path: p.Config(), Settings: map[string]any{}, Voices: Voices}
				for _, s := range all {
					view.Settings[s.key] = s.get(cfg)
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(view)
			}
			for _, s := range all {
				b, _ := json.Marshal(s.get(cfg))
				if _, err := fmt.Fprintf(out, "%s = %s\n", s.key, b); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(out, "\n(%s)\n", tilde(p.Config()))
			return err
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "print JSON, for the app")
	set := &cobra.Command{
		Use:   "set <setting> <value>",
		Short: "Change a setting, keeping the rest of config.toml as it is",
		Example: "  acmd config set notify.cooldown 1m\n  acmd config set -- claude_args \"--model opus\"\n" +
			"  acmd config set notify.sound \"\"   # silent",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findSetting(args[0])
			if err != nil {
				return err
			}
			value, err := s.parse(args[1])
			if err != nil {
				return err
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			section, name := s.section()
			if err := setup.SaveSetting(p.Config(), section, name, value); err != nil {
				return err
			}
			b, _ := json.Marshal(value)
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", s.key, b)
			return err
		},
	}
	cmd.AddCommand(show, set, &cobra.Command{
		Use:   "path",
		Short: "Print where config.toml is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), p.Config())
			return err
		},
	})
	return cmd
}
