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
	// Versions are the versions (ConfigState.Versions) of the settings the
	// client changes, as it loaded them: a key changed since refuses the
	// whole save with errcode.Conflict. A key left out applies whatever it is.
	Versions map[string]string `json:"versions,omitempty"`
}

// RemoteConfigSet changes a Remote host's settings.
type RemoteConfigSet struct {
	Host     string            `json:"host"`
	Values   map[string]any    `json:"values"`
	Versions map[string]string `json:"versions,omitempty"`
}

// ConfigState is the settings: what each one is, and its value in effect.
type ConfigState struct {
	// Path is config.toml, on the machine the settings are on.
	Path     string        `json:"path"`
	Settings []config.Spec `json:"settings"`
	// Values are in effect: a config.toml with a mistake leaves the last
	// good ones.
	Values map[string]any `json:"values"`
	// Versions are each setting's version, to send back with a change.
	Versions map[string]string `json:"versions"`
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

// HealthFix is how to put it right: an op a client can offer to call (with
// Data as its input), or, on the host itself, an acmd command to run
// (Command: one that needs the person there, such as a password), and Step
// in words either way.
type HealthFix struct {
	Op      string         `json:"op,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Command []string       `json:"command,omitempty"`
	Step    string         `json:"step,omitempty"`
	// Title is what a button for it says.
	Title string `json:"title,omitempty"`
}

// Health ids. An agent's hooks are HealthHooks + its harness name.
const (
	HealthConfigInvalid = "config.invalid"
	// HealthSetupOffer: hooks acmd needs are missing, so it offers to add them.
	HealthSetupOffer = "setup.offer"
	// HealthSetupRefresh: the hooks run an acmd that isn't this one (it moved):
	// setting up again fixes it without asking.
	HealthSetupRefresh = "setup.refresh"
	HealthHooks        = "hooks."
	HealthWake         = "wake"
	HealthHostingOff   = "remote.hosting_off"
)

// Severities, from the most serious: HealthError, HealthWarning, HealthInfo.
const HealthInfo = "info"

// Speech: device ops on this machine's daemon.
const (
	OpSpeechSay     = "speech.say"     // SpeechSay: reads it aloud, and answers when it's done or stopped
	OpSpeechStop    = "speech.stop"    // stops reading aloud, whatever started it
	OpSpeechStatus  = "speech.status"  // → SpeechState
	OpSpeechInstall = "speech.install" // sets speech up for the voice, in the background → SpeechState
)

// SpeechSay is the text to read aloud (Markdown: code is skipped).
type SpeechSay struct {
	Text string `json:"text"`
}

// SpeechState is how speech stands on this machine.
type SpeechState struct {
	Supported  bool   `json:"supported"`       // the helper is here
	Enabled    bool   `json:"enabled"`         // speech.enabled
	Installed  bool   `json:"installed"`       // set up for the voice
	Older      bool   `json:"older,omitempty"` // an older engine is set up instead
	Installing bool   `json:"installing,omitempty"`
	Progress   *int   `json:"progress,omitempty"` // percent downloaded, while installing
	Failure    string `json:"failure,omitempty"`  // why setting up failed
	Speaking   bool   `json:"speaking,omitempty"`
	Voice      string `json:"voice,omitempty"`
}

// Health ops.
const (
	OpHealthDismiss = "health.dismiss" // HealthRef: a client stops showing a warning → []string (its dismissed ids)
	OpHealthRestore = "health.restore" // HealthRef.ClientID: it shows them all again → []string
	OpInit          = "init"           // sets acmd up as `acmd init` does → InitResult
	OpReportDetails = "report.details" // ReportRequest.Kind → ReportDetails
	OpReportPage    = "report.page"    // ReportRequest → ReportPage
)

// HealthRef names a client and, to dismiss, a warning.
type HealthRef struct {
	ClientID string `json:"client_id"`
	ID       string `json:"id,omitempty"`
}

// SnapshotRequest is the snapshot's optional input: the client asking, for
// what it dismissed.
type SnapshotRequest struct {
	ClientID string `json:"client_id,omitempty"`
}

// InitResult is what setting up did, as acmd init says it.
type InitResult struct {
	Output string `json:"output"`
	Done   bool   `json:"done"`
}

// ReportRequest asks for a report: bug or feature, and for a page, its
// title and body.
type ReportRequest struct {
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// ReportDetails is what a report adds about the host: its version and OS,
// and for a bug, the end of its log, cleaned of secrets.
type ReportDetails struct {
	Details string `json:"details"`
}

// ReportPage is GitHub's page with the report filled in. Body is the full
// cleaned text, and Cut says the page holds less than that.
type ReportPage struct {
	PageURL string `json:"page_url"`
	Body    string `json:"body"`
	Cut     bool   `json:"cut,omitempty"`
}

// Features say what the host, and the device the daemon runs on, can do, so
// a client shows only what works.
type Features struct {
	Host   HostFeatures   `json:"host"`
	Device DeviceFeatures `json:"device"`
}

// HostFeatures: its OS, the agents installed, and whether it can wake for
// jobs and host Remote sessions.
type HostFeatures struct {
	OS      string   `json:"os"`     // runtime.GOOS: "darwin", "linux"
	Agents  []string `json:"agents"` // harnesses installed: claude, antigravity, terminal
	Wake    bool     `json:"wake"`
	Hosting bool     `json:"hosting"`
}

// DeviceFeatures: what the machine in front of the person can do.
type DeviceFeatures struct {
	Speech bool `json:"speech"` // the speech helper is there
	Editor bool `json:"editor"` // an editor is chosen
}

// Update is acmd's own version, and the newest release when it's newer.
type Update struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	URL     string `json:"url,omitempty"`
}

// ConfigOf is f's settings as config.get answers.
func ConfigOf(f *config.File) ConfigState {
	c := f.Config()
	return ConfigState{Path: f.Path(), Settings: config.Specs, Values: config.Values(c), Versions: config.Versions(c), Problem: f.Problem()}
}

// OpHarnesses lists what new sessions can run here (as the Remote API's
// harnesses.list does for a host), with an icon key for each. A client shows
// an icon key it doesn't know generically.
const OpHarnesses = "harnesses" // no arguments → Harnesses

// Harness is something a new session can run.
type Harness struct {
	Name  string `json:"name"`  // what new and job.save take: claude, antigravity, terminal
	Title string `json:"title"` // what to call it: "Claude", "Antigravity", "Terminal"
	Icon  string `json:"icon"`  // the client's icon for it; unknown ones are generic
	Agent bool   `json:"agent"` // keeps a conversation, runs jobs and can be resumed
}

// Harnesses are the harnesses installed, and the default for a plain new
// session ("" while it isn't chosen).
type Harnesses struct {
	Harnesses []Harness `json:"harnesses"`
	Default   string    `json:"default,omitempty"`
}
