package host

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// state is what Hosting keeps between runs that isn't config: the port the
// fallback picked, so clients find it there next time, and pending requests.
type state struct {
	Port     *rememberedPort      `json:"port,omitempty"`
	Requests []*ipc.RemoteRequest `json:"requests,omitempty"`
}

// rememberedPort is the port the fallback picked for a listen setting and
// port.
type rememberedPort struct {
	Listen     string `json:"listen"`
	Configured int    `json:"configured"`
	Port       int    `json:"port"`
}

func loadState(d Data) (state, error) {
	var st state
	b, err := d.HostingState()
	if err != nil || len(b) == 0 {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, fmt.Errorf("the Hosting state: %w", err)
	}
	return st, nil
}

// saveLocked keeps the state (h.mu is held). A failure is logged: the state
// in memory is still right.
func (h *Host) saveLocked() {
	b, err := json.Marshal(h.st)
	if err == nil {
		err = h.o.Data.SaveHostingState(b)
	}
	if err != nil {
		h.o.Log.Printf("remote: saving Hosting's state: %v", err)
	}
}

// Data is where Hosting keeps what changes through ops.
type Data interface {
	// AuthorizedClients are the clients authorized through ops, oldest first.
	AuthorizedClients() ([]config.AuthorizedClient, error)
	// AddAuthorizedClient adds a client, or renames the one with its
	// fingerprint.
	AddAuthorizedClient(config.AuthorizedClient) error
	// RemoveAuthorizedClient takes a client off, by its fingerprint.
	RemoveAuthorizedClient(fingerprint string) (bool, error)
	// HostingState and SaveHostingState keep the pending requests and the
	// port the fallback picked, as JSON.
	HostingState() ([]byte, error)
	SaveHostingState([]byte) error
}

// MemoryData is Data kept in memory, for tests and a Host with nowhere to
// keep it.
type MemoryData struct {
	mu      sync.Mutex
	Clients []config.AuthorizedClient
	State   []byte
	// Fail, when set, fails every change, as a full disk would.
	Fail error
}

func (m *MemoryData) AuthorizedClients() ([]config.AuthorizedClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.Clients), nil
}

func (m *MemoryData) AddAuthorizedClient(c config.AuthorizedClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	for i, have := range m.Clients {
		if have.Fingerprint == c.Fingerprint {
			m.Clients[i].Name = c.Name
			return nil
		}
	}
	m.Clients = append(m.Clients, c)
	return nil
}

func (m *MemoryData) RemoveAuthorizedClient(fp string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return false, m.Fail
	}
	n := len(m.Clients)
	m.Clients = slices.DeleteFunc(m.Clients, func(c config.AuthorizedClient) bool { return c.Fingerprint == fp })
	return len(m.Clients) != n, nil
}

func (m *MemoryData) HostingState() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.State), nil
}

func (m *MemoryData) SaveHostingState(b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.State = slices.Clone(b)
	return nil
}
