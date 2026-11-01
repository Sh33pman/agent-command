package daemon

import (
	"sort"
	"sync"

	"github.com/brettmostert/agent-command/internal/core"
)

// inbox holds the reply popup's items (SPEC §8.8): one per session, kept
// fresh by core.InboxEffect. Not persisted: a restarted daemon starts empty.
type inbox struct {
	mu sync.Mutex
	m  map[string]core.InboxItem
}

func (b *inbox) apply(s *core.Session, e core.Event) {
	item, drop := core.InboxEffect(s, e)
	switch {
	case item != nil:
		b.mu.Lock()
		if b.m == nil {
			b.m = map[string]core.InboxItem{}
		}
		b.m[s.ID] = *item
		b.mu.Unlock()
	case drop:
		b.drop(s.ID)
	}
}

func (b *inbox) drop(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, sessionID)
}

// all is the items, oldest first.
func (b *inbox) all() []core.InboxItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.m) == 0 {
		return nil
	}
	out := make([]core.InboxItem, 0, len(b.m))
	for _, it := range b.m {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}
