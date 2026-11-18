// Package wire is the protocol between two acmd daemons for Remote sessions:
// a host, which runs Hosting, and a client, which uses the host's Remote
// sessions. Both sides use it, so they always agree.
//
// # Connection
//
//  1. TCP, then mutual TLS 1.3. Each side shows its identity's cert
//     (internal/remote/identity), and each knows the other only by the cert's
//     fingerprint: there's no CA. The client pins the host's fingerprint and
//     aborts the handshake when it differs (*FingerprintError). The host takes
//     any client cert; it decides whether the client may in after the hello.
//  2. The hello: the client sends its Hello as one line of JSON, and the host
//     answers with a Welcome: its own Hello and a Status.
//     StatusOK: the client is authorized and speaks the host's protocol.
//     StatusPending: the host doesn't know this client, and has kept its
//     request for the user to approve (ErrPending: "waiting for approval").
//     StatusUnauthorized: refused outright, for example when too many
//     requests arrive (ErrUnauthorized).
//     StatusVersion: the two speak different protocols (*VersionError, which
//     names both versions).
//     Anything but StatusOK closes the connection on both sides.
//  3. yamux (github.com/hashicorp/yamux) over the TLS connection: the client
//     is yamux's client side. Every call opens a stream of its own.
//
// # Streams
//
// The side that opens a stream (the client) writes a Header, one line of JSON
// naming the op and its arguments. The other side answers with a Reply, one
// line of JSON: OK with data, or an error with a code (*CallError).
//
// A call (Conn.Call, Mux.Handle) ends there, and the stream is closed. A
// long-lived stream (Conn.Open, Mux.HandleStream) carries on after an OK
// reply, with whatever the op defines: lines of JSON for events (Stream.Send
// and Stream.Receive), or raw bytes for a session's terminal. Closing either
// end ends it, and closing the connection ends every stream on it.
//
// Proto is bumped on any change a peer of the previous version couldn't
// follow. Adding an op isn't one: a peer that doesn't know it answers
// CodeUnknownOp.
package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Proto is the protocol version both sides must speak.
const Proto = 1

// MaxLine bounds one line of JSON: a header, a reply or a hello.
const MaxLine = 4 << 20

// Hello introduces a side: the protocol it speaks, its acmd version and its
// machine's name.
type Hello struct {
	Proto   int    `json:"proto"`
	Version string `json:"version"`
	Name    string `json:"name,omitempty"`
}

// What the host makes of a client's hello.
const (
	StatusOK           = "ok"
	StatusPending      = "pending"
	StatusUnauthorized = "unauthorized"
	StatusVersion      = "version"
)

// Welcome is the host's answer to a client's hello.
type Welcome struct {
	Hello
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Header opens a stream: the op and its arguments.
type Header struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Reply answers a Header.
type Reply struct {
	OK    bool            `json:"ok"`
	Code  string          `json:"code,omitempty"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Error codes in a Reply.
const (
	CodeUnknownOp  = "unknown_op"  // the peer doesn't have the op
	CodeBadRequest = "bad_request" // the arguments don't make sense
	CodeNotFound   = "not_found"   // no such thing, or not one the client may see
	CodeInternal   = "internal"    // anything else
)

// ErrPending is a client the host doesn't know yet: it kept the request, and
// the client gets in once the user approves it there.
var ErrPending = errors.New("waiting for the host to approve this client")

// ErrUnauthorized is a client the host refused.
var ErrUnauthorized = errors.New("the host refused this client")

// VersionError is two sides that speak different protocols.
type VersionError struct {
	Ours, Theirs Hello
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("protocol mismatch: %s speaks Remote protocol %d (acmd %s), this acmd speaks %d (acmd %s): update the older one",
		nameOr(e.Theirs.Name, "the other side"), e.Theirs.Proto, nameOr(e.Theirs.Version, "unknown"), e.Ours.Proto, nameOr(e.Ours.Version, "unknown"))
}

func nameOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// FingerprintError is a host whose cert isn't the one the client pinned:
// it may be another machine, or the host made a new identity.
type FingerprintError struct {
	Want, Got string
}

func (e *FingerprintError) Error() string {
	return fmt.Sprintf("the host's fingerprint is %s, not %s as expected: it may be another machine, or the host made a new identity", e.Got, e.Want)
}

// CallError is an error the peer answered a call with.
type CallError struct {
	Op      string
	Code    string
	Message string
}

func (e *CallError) Error() string {
	if e.Op == "" {
		return e.Message
	}
	return e.Op + ": " + e.Message
}

// Errorf makes an error that reaches the client with code.
func Errorf(code, format string, a ...any) error {
	return &CallError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// IsCode says whether err is a CallError with code.
func IsCode(err error, code string) bool {
	var ce *CallError
	return errors.As(err, &ce) && ce.Code == code
}

// writeLine writes v as one line of JSON.
func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// readLine reads one line of JSON into v.
func readLine(r *bufio.Reader, v any) error {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return err
		}
		buf = append(buf, chunk...)
		if len(buf) > MaxLine {
			return errors.New("message too large")
		}
		if !isPrefix {
			break
		}
	}
	d := json.NewDecoder(bytes.NewReader(buf))
	return d.Decode(v)
}

// marshal is v as JSON, nil staying nil.
func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}
