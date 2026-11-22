// Package catalog is the single list of the daemon's ops: for each, its name,
// what it does, its input and output types, the scope a caller needs, what
// kind of change it makes, and how a caller's grant limits it.
//
// Everything that describes the protocol to someone else is generated from
// here: the JSON Schema (Schema), and later MCP tool definitions, an OpenAPI
// document and CLI help. A new op is registered here when it's added.
//
// Protocol 1 still sends the change ops as one "mutate" op with a kind
// (ipc.Mutation). The catalog lists each of those kinds as an op of its own,
// with Mutation set, so each has its own scope and kind.
package catalog

import (
	"errors"
	"fmt"
	"sort"
)

// Scope is what a caller must be allowed to do to call an op.
type Scope string

const (
	// ScopeRead lists and reads everything, and subscribes to events.
	ScopeRead Scope = "read"
	// ScopeAct starts, attaches to and types into sessions, answers the
	// Inbox, renames, archives and moves.
	ScopeAct Scope = "act"
	// ScopeAdmin changes settings and Hosting, approves clients, purges, and
	// changes what runs.
	ScopeAdmin Scope = "admin"
)

// Kind is what an op does to the host's state.
type Kind string

const (
	KindRead        Kind = "read"        // changes nothing
	KindChange      Kind = "change"      // changes something that can be changed back
	KindDestructive Kind = "destructive" // deletes or loses something for good
)

// Grant is how a caller's grant limits an op. A grant is everything on a host
// (the local socket, a Direct Connect device) or, later, a share of some
// entities.
type Grant string

const (
	// GrantNone: any authenticated caller, whatever its grant (the
	// handshake).
	GrantNone Grant = "none"
	// GrantHost: only a caller granted the whole host. A partial grant is
	// refused.
	GrantHost Grant = "host"
	// GrantEntities: results are filtered to the entities the caller is
	// granted, and every entity the op targets must be granted.
	GrantEntities Grant = "entities"
)

// Op is one op in the catalog.
type Op struct {
	// Name is the op's name, as clients send it ("session.rename").
	Name string
	// Description says what it does, for people and for generated tools.
	Description string
	// Input and Output are values of the op's argument and result types (a
	// zero value is enough). nil means it takes or returns nothing.
	Input, Output any
	Scope         Scope
	Kind          Kind
	Grant         Grant
	// HostOnly: only callers on the host itself, never over the network
	// (hooks, stopping the daemon, the admin password for waking).
	HostOnly bool
	// RunsCode: it starts a process on the host or types into one, so a
	// caller such as an MCP client can be kept from it unless allowed.
	RunsCode bool
	// Stream: after its reply, the connection carries a stream instead of
	// more requests.
	Stream bool
	// Mutation: protocol 1 sends it as ipc.OpMutate, with Name as the kind.
	Mutation bool
	// Retryable: it creates something, and takes a request id, so a retry
	// with the same id gets the first result instead of a second one.
	Retryable bool
	// OwnHosts: it's this machine using its own Remote hosts (remote.hosts,
	// remote.call, …), so a paired machine can't use a host to reach yet
	// another.
	OwnHosts bool
}

// MutateOp is the protocol-1 op that carries every Mutation op.
const MutateOp = "mutate"

// Transport is how protocol 1 sends the op: the op name on the wire, and the
// mutation kind ("" unless it's a Mutation).
func (o Op) Transport() (op, kind string) {
	if o.Mutation {
		return MutateOp, o.Name
	}
	return o.Name, ""
}

// Catalog is a checked set of ops.
type Catalog struct {
	ops    []Op // by name
	byName map[string]int
}

// New checks ops and returns them as a catalog.
func New(ops ...Op) (*Catalog, error) {
	c := &Catalog{byName: make(map[string]int, len(ops))}
	var errs []error
	for _, o := range ops {
		if err := o.check(); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, dup := c.byName[o.Name]; dup {
			errs = append(errs, fmt.Errorf("op %q: registered twice", o.Name))
			continue
		}
		c.byName[o.Name] = len(c.ops)
		c.ops = append(c.ops, o)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(c.ops, func(i, j int) bool { return c.ops[i].Name < c.ops[j].Name })
	for i, o := range c.ops {
		c.byName[o.Name] = i
	}
	return c, nil
}

func (o Op) check() error {
	bad := func(format string, a ...any) error {
		name := o.Name
		if name == "" {
			name = "(no name)"
		}
		return fmt.Errorf("op %q: %s", name, fmt.Sprintf(format, a...))
	}
	switch {
	case o.Name == "":
		return bad("no name")
	case o.Name == MutateOp:
		return bad("%q is protocol 1's carrier for Mutation ops, not an op", MutateOp)
	case o.Description == "":
		return bad("no description")
	}
	switch o.Scope {
	case ScopeRead, ScopeAct, ScopeAdmin:
	default:
		return bad("unknown scope %q", o.Scope)
	}
	switch o.Kind {
	case KindRead, KindChange, KindDestructive:
	default:
		return bad("unknown kind %q", o.Kind)
	}
	switch o.Grant {
	case GrantNone, GrantHost, GrantEntities:
	default:
		return bad("unknown grant %q", o.Grant)
	}
	if o.Kind != KindRead && o.Scope == ScopeRead {
		return bad("a %s op needs more than the read scope", o.Kind)
	}
	if o.Grant == GrantNone && o.Kind != KindRead {
		return bad("only read ops can ignore the caller's grant")
	}
	if o.RunsCode && o.Scope == ScopeRead {
		return bad("an op that runs code needs more than the read scope")
	}
	if o.Mutation && o.Stream {
		return bad("a Mutation op can't be a stream")
	}
	return nil
}

// Ops returns every op, by name.
func (c *Catalog) Ops() []Op { return append([]Op(nil), c.ops...) }

// Names returns every op's name, sorted: the host's op list.
func (c *Catalog) Names() []string {
	names := make([]string, len(c.ops))
	for i, o := range c.ops {
		names[i] = o.Name
	}
	return names
}

// Lookup finds an op by name.
func (c *Catalog) Lookup(name string) (Op, bool) {
	i, ok := c.byName[name]
	if !ok {
		return Op{}, false
	}
	return c.ops[i], true
}
