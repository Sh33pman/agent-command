package ipc

import "github.com/brettmostert/agent-command/internal/config"

// Settings: config.toml, through the daemon, its only writer.
const (
	OpConfigGet = "config.get" // no arguments → ConfigState
	OpConfigSet = "config.set" // ConfigSet → ConfigState
	// On a Remote host: its host settings, not its device ones.
	OpRemoteConfigGet = "remote.config.get" // RemoteHostRef.Host → ConfigState
	OpRemoteConfigSet = "remote.config.set" // RemoteConfigSet → ConfigState
)

// ConfigSet changes settings by key, all or none. A value is what the
// setting's type takes in JSON (true, 8080, "10s", ["--model", "opus"]), or
// text as it's typed ("on", "8080").
type ConfigSet struct {
	Values map[string]any `json:"values"`
}

// RemoteConfigSet changes a Remote host's settings.
type RemoteConfigSet struct {
	Host   string         `json:"host"`
	Values map[string]any `json:"values"`
}

// ConfigState is the settings: what each one is, and its value in effect.
type ConfigState struct {
	// Path is config.toml, on the machine the settings are on.
	Path     string        `json:"path"`
	Settings []config.Spec `json:"settings"`
	// Values are in effect: a config.toml with a mistake leaves the last
	// good ones.
	Values map[string]any `json:"values"`
	// Problem is what's wrong with config.toml, while changes are refused.
	Problem *config.Problem `json:"problem,omitempty"`
}

// Health is something wrong, or worth knowing, about the host.
type Health struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // HealthError or HealthWarning
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	// Fix is how to put it right.
	Fix *HealthFix `json:"fix,omitempty"`
}

// Severities.
const (
	HealthError   = "error"
	HealthWarning = "warning"
)

// HealthFix is an op a client can offer to call, or a step to take on the
// host.
type HealthFix struct {
	Op   string `json:"op,omitempty"`
	Step string `json:"step,omitempty"`
}

// Health ids.
const HealthConfigInvalid = "config.invalid"

// ConfigOf is f's settings as config.get answers.
func ConfigOf(f *config.File) ConfigState {
	return ConfigState{Path: f.Path(), Settings: config.Specs, Values: config.Values(f.Config()), Problem: f.Problem()}
}
