// Package errcode is the one list of error codes every transport answers
// with: the local socket, the Remote API, and later HTTP. A client decides
// what to do from the code, never from the message, which is for people.
package errcode

// The codes. Each is stable: one is added, never renamed or reused.
const (
	NotFound         = "not_found"         // no such thing, or not one the caller may see
	Invalid          = "invalid"           // it can't be done to this one: unarchive a terminal, purge one not archived
	BadRequest       = "bad_request"       // the request doesn't make sense: arguments that don't decode, or are missing
	Conflict         = "conflict"          // it changed since the caller loaded it: reload, and try again
	PermissionDenied = "permission_denied" // the caller's scope, or the host's user, may not do that
	Forbidden        = "forbidden"         // a paired device may not call that op at all
	NotRunning       = "not_running"       // the session's terminal has ended
	NotSupported     = "not_supported"     // the session's host can't do that yet
	HostUnreachable  = "host_unreachable"  // its Remote host can't be reached now
	UnknownOp        = "unknown_op"        // the daemon doesn't have the op
	ProtoUnsupported = "proto_unsupported" // no protocol version both sides speak
	Exists           = "exists"            // there's already something at that path
	NotFolder        = "not_folder"        // the path is a file, not a folder
	Internal         = "internal"          // anything else: the message says what
)

// Code is one code and what it means, for the catalog.
type Code struct {
	Code    string `json:"code"`
	Meaning string `json:"meaning"`
}

// All is every code, in the catalog's order.
var All = []Code{
	{NotFound, "No such thing, or not one the caller may see."},
	{Invalid, "It can't be done to this one, such as unarchiving a terminal."},
	{BadRequest, "The request doesn't make sense: its arguments don't decode, or are missing."},
	{Conflict, "It changed since the caller loaded it: reload, and try again."},
	{PermissionDenied, "The caller's scope, or the host's user, may not do that."},
	{Forbidden, "A paired device may not call that op at all."},
	{NotRunning, "The session's terminal has ended."},
	{NotSupported, "The session's host can't do that yet."},
	{HostUnreachable, "Its Remote host can't be reached now."},
	{UnknownOp, "The daemon doesn't have the op."},
	{ProtoUnsupported, "No protocol version both sides speak."},
	{Exists, "There's already something at that path."},
	{NotFolder, "The path is a file, not a folder."},
	{Internal, "Anything else: the message says what."},
}

// Known says whether code is one of All.
func Known(code string) bool {
	for _, c := range All {
		if c.Code == code {
			return true
		}
	}
	return false
}
