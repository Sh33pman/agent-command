package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/etag"
	"github.com/brettmostert/agent-command/internal/speech"
)

// Settings are described here, once, for every client: the daemon validates
// config.set with them and sends them to clients with config.get, so a form
// can be drawn from them and nothing outside acmd parses config.toml.

// Where a setting belongs.
const (
	// WhereHost settings say how the host behaves, wherever it's used from.
	WhereHost = "host"
	// WhereDevice settings belong to the machine the person is using: the
	// editor and speech. A Remote client can't see or change a host's.
	WhereDevice = "device"
)

// Types of setting, as Spec.Type says them.
const (
	TypeBool     = "bool"
	TypeString   = "string"
	TypeInt      = "int"
	TypeNumber   = "number"
	TypeDuration = "duration" // a Go duration: "10s", "1m30s"
	TypeList     = "list"     // a list of strings
	TypeChoice   = "choice"   // one of Choices
)

// Spec describes one setting.
type Spec struct {
	Key         string   `json:"key"` // "notify.cooldown": cooldown in [notify]
	Type        string   `json:"type"`
	Default     any      `json:"default"`
	Choices     []string `json:"choices,omitempty"`     // TypeChoice: what it may be
	Suggestions []string `json:"suggestions,omitempty"` // values a form may offer, such as the voices
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Description string   `json:"description"`
	Where       string   `json:"where"` // WhereHost or WhereDevice
	// RunsCode settings choose what runs on the host: changing them needs the
	// admin scope.
	RunsCode bool `json:"runs_code,omitempty"`
	// ReadOnly settings are written by acmd itself, such as the editor's
	// launcher once it's found.
	ReadOnly bool `json:"read_only,omitempty"`

	get   func(Config) any
	check func(any) (any, error) // after the type's own check
}

// Section and Name split Key at its last dot: "remote.host.port" is port in
// [remote.host].
func (s Spec) Section() string {
	if i := strings.LastIndex(s.Key, "."); i >= 0 {
		return s.Key[:i]
	}
	return ""
}

func (s Spec) Name() string { return s.Key[strings.LastIndex(s.Key, ".")+1:] }

// Value is the setting's value in c, as JSON shows it.
func (s Spec) Value(c Config) any { return s.get(c) }

// Voices are Kokoro's American English voices, the ones its model can speak.
var Voices = []string{
	"af_heart", "af_alloy", "af_aoede", "af_bella", "af_jessica", "af_kore", "af_nicole", "af_nova", "af_river",
	"af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael", "am_onyx",
	"am_puck", "am_santa",
}

// Harnesses that can be the default.
const (
	HarnessClaude      = "claude"
	HarnessAntigravity = "antigravity"
)

func num(f float64) *float64 { return &f }

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func notEmpty(key string) func(any) (any, error) {
	return func(v any) (any, error) {
		s := strings.TrimSpace(v.(string))
		if s == "" {
			return nil, fmt.Errorf("%s can't be empty", key)
		}
		return s, nil
	}
}

// Specs are every setting, in the order a form shows them.
var Specs = []Spec{
	{Key: "claude_command", Type: TypeString, Default: "claude", Where: WhereHost, RunsCode: true,
		Description: "The claude binary that agent sessions run. acmd init finds it.",
		get:         func(c Config) any { return c.ClaudeCommand }, check: notEmpty("claude_command")},
	{Key: "claude_args", Type: TypeList, Default: []string{}, Where: WhereHost, RunsCode: true,
		Description: "Extra arguments for new Claude sessions.",
		get:         func(c Config) any { return orEmpty(c.ClaudeArgs) }},
	{Key: "agy_command", Type: TypeString, Default: "agy", Where: WhereHost, RunsCode: true,
		Description: "Antigravity's CLI, agy, that its sessions run. acmd init finds it.",
		get:         func(c Config) any { return c.AgyCommand }, check: notEmpty("agy_command")},
	{Key: "agy_args", Type: TypeList, Default: []string{}, Where: WhereHost, RunsCode: true,
		Description: "Extra arguments for new Antigravity sessions.",
		get:         func(c Config) any { return orEmpty(c.AgyArgs) }},
	{Key: "agent_remote_control", Type: TypeBool, Default: false, Where: WhereHost,
		Description: "Start agent sessions with --remote-control, so they can be used from claude.ai, the Claude app or Antigravity's browser view. Applies to sessions started or resumed after a change.",
		get:         func(c Config) any { return c.AgentRemoteControl }},
	{Key: "default_harness", Type: TypeChoice, Default: "", Choices: []string{HarnessClaude, HarnessAntigravity, ""}, Where: WhereHost,
		Description: "What a plain new session runs. Empty asks in the app the first time, and acmd new starts Claude.",
		get:         func(c Config) any { return c.DefaultHarness }},

	{Key: "notify.enabled", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Notifications, for sessions that need you, fail, finish or stop.",
		get:         func(c Config) any { return c.Notify.Enabled }},
	{Key: "notify.on_waiting", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Notify when a session waits for you.", get: func(c Config) any { return c.Notify.OnWaiting }},
	{Key: "notify.on_error", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Notify when a session fails.", get: func(c Config) any { return c.Notify.OnError }},
	{Key: "notify.on_done", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Notify when a session finishes its turn.", get: func(c Config) any { return c.Notify.OnDone }},
	{Key: "notify.on_paused", Type: TypeBool, Default: false, Where: WhereHost,
		Description: "Notify when a session stops.", get: func(c Config) any { return c.Notify.OnPaused }},
	{Key: "notify.done_min_duration", Type: TypeDuration, Default: "10s", Where: WhereHost,
		Description: "Only say a turn finished when it took at least this long.",
		get:         func(c Config) any { return c.Notify.DoneMinDuration }},
	{Key: "notify.cooldown", Type: TypeDuration, Default: "20s", Where: WhereHost,
		Description: "At most one notification per session in this long.",
		get:         func(c Config) any { return c.Notify.Cooldown }},
	{Key: "notify.sound", Type: TypeChoice, Default: "default", Choices: []string{"default", ""}, Where: WhereHost,
		Description: `"default" plays the notification sound; empty is silent.`,
		get:         func(c Config) any { return c.Notify.Sound }},

	{Key: "notify.route", Type: TypeChoice, Default: RouteOne, Choices: []string{RouteOne, RouteAll}, Where: WhereHost,
		Description: `Which clients show a notice: "one" shows it on the one you're using (else the desktop app you used last, else the host's own notification); "all" on every connected one. A paired machine counts as one client, as you use its apps, and shows it on its app you're using.`,
		get:         func(c Config) any { return c.Notify.Route }},
	{Key: "clients.forget_after", Type: TypeDuration, Default: "720h", Where: WhereHost,
		Description: `Forget an app or other client not seen for this long: its layout, notification choices and dismissed warnings. "0" keeps them for good.`,
		get:         func(c Config) any { return c.Clients.ForgetAfter }},

	{Key: "inbox.permission_answers", Type: TypeChoice, Default: AnswersPeople,
		Choices: []string{AnswersPeople, AnswersOutsideSessions, AnswersAnyone}, Where: WhereHost,
		Description: `Who may answer a permission prompt on this machine: "people" refuses an answer from a program an agent (claude, agy) started; "outside_sessions" any answer from inside an acmd session; "anyone" none. Remote clients go by their pairing.`,
		get:         func(c Config) any { return c.Inbox.PermissionAnswers }},

	{Key: "speech.enabled", Type: TypeBool, Default: false, Where: WhereDevice,
		Description: "Read aloud on this machine: speech buttons, and the model downloaded when it's turned on.",
		get:         func(c Config) any { return c.Speech.Enabled }},
	{Key: "speech.voice", Type: TypeString, Default: "af_heart:0.6,af_bella:0.4", Suggestions: Voices, Where: WhereDevice,
		Description: `Kokoro's voice, or a blend "voice:weight,voice:weight". American English voices only (af_, am_).`,
		get:         func(c Config) any { return c.Speech.Voice },
		check: func(v any) (any, error) {
			s := strings.TrimSpace(v.(string))
			return s, speech.CheckVoice(s)
		}},
	{Key: "speech.speed", Type: TypeNumber, Default: 1.0, Min: num(0.5), Max: num(2), Where: WhereDevice,
		Description: "How fast speech reads.", get: func(c Config) any { return c.Speech.Speed }},

	{Key: "editor.preset", Type: TypeChoice, Default: "", Choices: []string{"vscode", "cursor", "zed", "idea", "custom", ""}, Where: WhereDevice,
		Description: "The editor that opens session folders. Empty asks on first use.",
		get:         func(c Config) any { return c.Editor.Preset }},
	{Key: "editor.command", Type: TypeList, Default: []string{}, Where: WhereDevice, RunsCode: true,
		Description: `For a custom editor: its command, with {dir} for the folder, such as ["nvim", "{dir}"].`,
		get:         func(c Config) any { return orEmpty(c.Editor.Command) }},
	{Key: "editor.resolved", Type: TypeString, Default: "", Where: WhereDevice, ReadOnly: true,
		Description: "The editor's launcher, as acmd found it.", get: func(c Config) any { return c.Editor.Resolved }},
	{Key: "editor.run_in", Type: TypeChoice, Default: "background", Choices: []string{"background", "tab"}, Where: WhereDevice,
		Description: "background for GUI editors; tab for terminal editors.", get: func(c Config) any { return c.Editor.RunIn }},
	{Key: "editor.target", Type: TypeChoice, Default: "cwd", Choices: []string{"cwd", "git_root"}, Where: WhereDevice,
		Description: "Open the session's folder, or its git repository's root.", get: func(c Config) any { return c.Editor.Target }},

	{Key: "updates.check", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Look for newer releases of acmd every few hours. Off makes no request at all.",
		get:         func(c Config) any { return c.Updates.Check }},

	{Key: "remote.new_sessions_remote", Type: TypeBool, Default: false, Where: WhereHost,
		Description: "Whether a new session is Remote unless it says otherwise.",
		get:         func(c Config) any { return c.Remote.NewSessionsRemote }},
	{Key: "remote.host.enabled", Type: TypeBool, Default: false, Where: WhereHost,
		Description: "Hosting: let authorized clients use this machine's Remote sessions.",
		get:         func(c Config) any { return c.Remote.Host.Enabled }},
	{Key: "remote.host.listen", Type: TypeChoice, Default: ListenLocalhost, Choices: []string{ListenLocalhost, ListenAll}, Where: WhereHost,
		Description: `Where Hosting listens: "localhost" (127.0.0.1) or "all" (0.0.0.0).`,
		get:         func(c Config) any { return c.Remote.Host.Listen }},
	{Key: "remote.host.port", Type: TypeInt, Default: DefaultRemotePort, Min: num(1), Max: num(65535), Where: WhereHost,
		Description: "Hosting's port.", get: func(c Config) any { return c.Remote.Host.Port }},
	{Key: "remote.host.port_fallback", Type: TypeBool, Default: true, Where: WhereHost,
		Description: "Use another port when this one is taken.", get: func(c Config) any { return c.Remote.Host.PortFallback }},
	{Key: "remote.host.cert", Type: TypeString, Default: "", Where: WhereHost,
		Description: "The cert Hosting shows clients. Empty is acmd's generated identity.",
		get:         func(c Config) any { return c.Remote.Host.Cert }},
	{Key: "remote.host.key", Type: TypeString, Default: "", Where: WhereHost,
		Description: "The key of remote.host.cert.", get: func(c Config) any { return c.Remote.Host.Key }},
	{Key: "remote.client.cert", Type: TypeString, Default: "", Where: WhereHost,
		Description: "The cert this machine shows the hosts it connects to. Empty is acmd's generated identity.",
		get:         func(c Config) any { return c.Remote.Client.Cert }},
	{Key: "remote.client.key", Type: TypeString, Default: "", Where: WhereHost,
		Description: "The key of remote.client.cert.", get: func(c Config) any { return c.Remote.Client.Key }},
}

// oldKeys are settings' earlier names.
var oldKeys = map[string]string{oldAgentRemoteControlKey: agentRemoteControlKey}

// Find finds a setting by its key, or its earlier name.
func Find(key string) (Spec, bool) {
	if k, ok := oldKeys[key]; ok {
		key = k
	}
	i := slices.IndexFunc(Specs, func(s Spec) bool { return s.Key == key })
	if i < 0 {
		return Spec{}, false
	}
	return Specs[i], true
}

// Values are every setting's value in c, by key.
func Values(c Config) map[string]any {
	out := make(map[string]any, len(Specs))
	for _, s := range Specs {
		out[s.Key] = s.get(c)
	}
	return out
}

// Versions are each setting's version in c, by key: a hash of its value, so
// a client can say which it loaded (File.SetIf).
func Versions(c Config) map[string]string {
	out := make(map[string]string, len(Specs))
	for key, v := range Values(c) {
		out[key] = etag.Of(v)
	}
	return out
}

// ErrChanged is a setting that changed since the client loaded it: by
// another client, or by hand in config.toml.
var ErrChanged = errors.New("changed since you loaded it")

// changedSince is ErrChanged, naming the settings whose version in c isn't
// the one in versions.
func changedSince(c Config, versions map[string]string) error {
	if len(versions) == 0 {
		return nil
	}
	now := Versions(c)
	var keys []string
	for key, v := range versions {
		if now[key] != v {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	slices.Sort(keys)
	return fmt.Errorf("%s %w", strings.Join(keys, ", "), ErrChanged)
}

// ErrUnknownSetting is a key that isn't a setting.
var ErrUnknownSetting = errors.New("no such setting")

// Check validates v for the setting and returns it as config.toml keeps it.
// v is what JSON decodes to (bool, float64, string, []any), or text as it's
// typed: "on", "10s", "8080", `["--model", "opus"]` or words for a list.
func (s Spec) Check(v any) (any, error) {
	if n, ok := v.(json.Number); ok {
		v = n.String()
	}
	out, err := s.checkType(v)
	if err != nil {
		return nil, err
	}
	if s.check != nil {
		return s.check(out)
	}
	return out, nil
}

func (s Spec) checkType(v any) (any, error) {
	text, isText := v.(string)
	switch s.Type {
	case TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "on", "yes", "1":
			return true, nil
		case "false", "off", "no", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%s is on or off, not %s", s.Key, show(v))
	case TypeString:
		if !isText {
			return nil, fmt.Errorf("%s is text, not %s", s.Key, show(v))
		}
		return text, nil
	case TypeChoice:
		if !isText || !slices.Contains(s.Choices, text) {
			return nil, fmt.Errorf("%s is one of %s, not %s", s.Key, choices(s.Choices), show(v))
		}
		return text, nil
	case TypeDuration:
		d, err := time.ParseDuration(strings.TrimSpace(text))
		if !isText || err != nil || d < 0 {
			return nil, fmt.Errorf("%s is a length of time like 10s or 2m, not %s", s.Key, show(v))
		}
		return d.String(), nil
	case TypeInt, TypeNumber:
		f, ok := number(v)
		if !ok || (s.Type == TypeInt && f != math.Trunc(f)) || (s.Min != nil && f < *s.Min) || (s.Max != nil && f > *s.Max) {
			return nil, fmt.Errorf("%s is %s, not %s", s.Key, s.rangeText(), show(v))
		}
		if s.Type == TypeInt {
			return int(f), nil
		}
		return f, nil
	case TypeList:
		return list(s.Key, v)
	}
	return nil, fmt.Errorf("%s: unknown type %s", s.Key, s.Type)
}

func (s Spec) rangeText() string {
	what := "a number"
	if s.Type == TypeInt {
		what = "a whole number"
	}
	if s.Min != nil && s.Max != nil {
		return fmt.Sprintf("%s from %g to %g", what, *s.Min, *s.Max)
	}
	return what
}

func number(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// list takes a JSON list of strings, or text: a JSON array, or words split
// at spaces.
func list(key string, v any) (any, error) {
	switch v := v.(type) {
	case []string:
		return orEmpty(slices.Clone(v)), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s is a list of text, not one with %s", key, show(e))
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "[") {
			var out []string
			if err := json.Unmarshal([]byte(v), &out); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			return orEmpty(out), nil
		}
		return orEmpty(strings.Fields(v)), nil
	}
	return nil, fmt.Errorf("%s is a list, not %s", key, show(v))
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func choices(list []string) string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = strconv.Quote(c)
	}
	return strings.Join(out, ", ")
}
