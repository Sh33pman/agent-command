package catalog

import (
	"fmt"

	"github.com/brettmostert/agent-command/internal/errcode"
)

// Caller is who runs an op, as its transport authenticated it (design 6.12,
// 9): the user on this machine's socket, or a paired machine over mTLS with
// its pairing's scope. Every transport checks ops with Allow, so the rules
// live here, in one place.
type Caller struct {
	// Local: on this machine's own socket. Its scope is everything; its
	// guardrails (inbox.permission_answers) apply in the daemon.
	Local bool
	// Scope is what a paired machine's pairing allows.
	Scope Scope
	// Name names the caller in a refusal: the pairing, for a paired machine.
	Name string
}

// DeniedError is an op a caller may not run, with its code.
type DeniedError struct {
	code, msg string
}

func (e *DeniedError) Error() string { return e.msg }

// Code is the error's code: errcode.Forbidden for an op no paired machine
// may run, errcode.PermissionDenied for one its scope doesn't allow.
func (e *DeniedError) Code() string { return e.code }

// rank orders scopes: each allows what the ones below it do.
func rank(s Scope) int {
	return map[Scope]int{ScopeRead: 1, ScopeAct: 2, ScopeAdmin: 3}[s]
}

// Allow says whether c may run op: nil, or a *DeniedError. On this machine
// everything is allowed. From another machine, an op for the host's own
// machine (HostOnly) or for its own hosts (OwnHosts) never is, and the rest
// need the pairing's scope. Every caller has the whole host today (Direct
// Connect), so the op's grant is met; Sharing will check it here.
func (c Caller) Allow(op Op) error {
	if c.Local {
		return nil
	}
	if op.HostOnly || op.OwnHosts {
		return &DeniedError{code: errcode.Forbidden, msg: fmt.Sprintf("%s can't be run from another machine", op.Name)}
	}
	if rank(c.Scope) < rank(op.Scope) {
		return &DeniedError{code: errcode.PermissionDenied,
			msg: fmt.Sprintf("%s needs %s, and this machine may only %s on the host: there, acmd remote scope %s %s",
				op.Name, op.Scope, c.Scope, c.Name, op.Scope)}
	}
	return nil
}
