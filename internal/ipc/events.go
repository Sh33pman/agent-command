package ipc

import "encoding/json"

// The event stream (design 8.3): a client subscribes and gets a snapshot,
// then the entities that are added, updated (sent whole) or removed, each
// with a revision. No field-level diffs.

// OpSubscribe starts the stream (SubscribeRequest → a stream of Event lines):
// after its OK reply the connection carries one Event per line until either
// end closes it.
const OpSubscribe = "subscribe"

// SubscribeRequest says what a client wants: the entity types (Entity*), all
// of them when none; and Since, the last revision it has, to resume after a
// dropped connection. The daemon replays what it missed from a short log, or
// sends a new snapshot when it's too far behind.
type SubscribeRequest struct {
	Types []string `json:"types,omitempty"`
	Since uint64   `json:"since,omitempty"`
}

// Event is one line of the stream.
type Event struct {
	// Rev is the revision after this event: send it back as Since to resume.
	Rev  uint64 `json:"rev"`
	Type string `json:"type"` // an Event* type
	// Entity and ID name what changed; Data is it, whole, for added and
	// updated. A single value (view, settings, …) has no ID.
	Entity string          `json:"entity,omitempty"`
	ID     string          `json:"id,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	// Snapshot is everything, for EventSnapshot: what the client wants of it.
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}

// Event types. EventResync: the client fell behind; a new snapshot follows,
// and it should replace what it has.
const (
	EventSnapshot = "snapshot"
	EventAdded    = "added"
	EventUpdated  = "updated"
	EventRemoved  = "removed"
	EventResync   = "resync"
)

// Entity types in the stream. The first ones are lists, each entity by id;
// the rest are single values of the snapshot, updated whole.
const (
	EntitySession    = "session"
	EntityGroup      = "group"
	EntityInbox      = "inbox"
	EntityJob        = "job"
	EntityNotice     = "notice"
	EntityRemoteHost = "remote_host"

	EntityView      = "view"
	EntitySettings  = "settings"
	EntityHealth    = "health"
	EntityDismissed = "dismissed"
	EntityFeatures  = "features"
	EntityUpdate    = "update"
	EntitySpeech    = "speech"
	EntityRemote    = "remote"
	EntityWake      = "wake"
)

// EntityTypes are every entity type, in the snapshot's order.
var EntityTypes = []string{EntitySession, EntityGroup, EntitySet, EntityInbox, EntityJob, EntityNotice, EntityRemoteHost,
	EntityView, EntitySettings, EntityHealth, EntityDismissed, EntityFeatures, EntityUpdate, EntitySpeech, EntityRemote, EntityWake}

// Subscribe turns the connection into an event stream: read it with
// NextEvent. Nothing else can be called on it after.
func (c *Conn) Subscribe(req SubscribeRequest) error {
	return c.Call(OpSubscribe, req, nil)
}

// NextEvent reads the stream's next event.
func (c *Conn) NextEvent() (Event, error) {
	var ev Event
	err := ReadJSON(c.r, &ev)
	return ev, err
}
