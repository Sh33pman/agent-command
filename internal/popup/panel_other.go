//go:build !darwin || !cgo

package popup

// Run is unavailable without the native panel.
func Run(func(Event)) error { return ErrUnsupported }

// Update does nothing without the native panel.
func Update(State) {}

// SetBindings does nothing without the native panel.
func SetBindings([]Binding) {}

// RegisterHotKey is unavailable without the native panel.
func RegisterHotKey(int, string) error { return ErrUnsupported }

// Supported reports whether this build has the native panel.
func Supported() bool { return false }

// Frame is empty without the native panel.
func Frame() string { return "" }

// FrontmostApp is unknown without the native panel.
func FrontmostApp() string { return "" }

// SelfTest is unavailable without the native panel.
func SelfTest(func()) string { return "" }
