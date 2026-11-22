package wire

// Ops every host serves.
const (
	OpPing = "ping" // no arguments, no data: the connection works
	OpInfo = "info" // no arguments → Info
)

// Info is what a host says about itself.
type Info struct {
	Name        string   `json:"name"`        // its machine's name
	Fingerprint string   `json:"fingerprint"` // its identity's
	Version     string   `json:"version"`     // its acmd
	Proto       int      `json:"proto"`
	API         int      `json:"api,omitempty"`     // the newest host API version it speaks (internal/remote/api)
	APIMin      int      `json:"api_min,omitempty"` // the oldest it still speaks
	Ops         []string `json:"ops"`               // the ops it serves
}
