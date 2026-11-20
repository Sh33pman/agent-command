package catalog

import "fmt"

// Range is the protocol versions one side speaks, from Min to Max.
//
// Each side sends its range in its hello, and both use the highest version
// in both ranges (Negotiate). Clients then decide what to show from the
// host's op list and the fields they receive, never from the version (D6).
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Valid reports whether r is a range: versions start at 1, and Min ≤ Max.
func (r Range) Valid() bool { return r.Min >= 1 && r.Min <= r.Max }

func (r Range) String() string {
	if r.Min == r.Max {
		return fmt.Sprint(r.Min)
	}
	return fmt.Sprintf("%d–%d", r.Min, r.Max)
}

// CodeProtoUnsupported is the error code for two sides with no version in
// common.
const CodeProtoUnsupported = "proto_unsupported"

// ProtoError is why two sides can't talk: no version in common, or a range
// that isn't one.
type ProtoError struct {
	Mine, Theirs Range
}

// Code is the error's stable code (8.5).
func (e *ProtoError) Code() string { return CodeProtoUnsupported }

// Update says which side is older and should be updated: "theirs", "mine",
// or "" when a range isn't valid.
func (e *ProtoError) Update() string {
	switch {
	case !e.Mine.Valid() || !e.Theirs.Valid():
		return ""
	case e.Theirs.Max < e.Mine.Min:
		return "theirs"
	default:
		return "mine"
	}
}

func (e *ProtoError) Error() string {
	switch e.Update() {
	case "theirs":
		return fmt.Sprintf("the other side speaks protocol %s and this one %s: update the other side", e.Theirs, e.Mine)
	case "mine":
		return fmt.Sprintf("the other side speaks protocol %s and this one %s: update this side", e.Theirs, e.Mine)
	}
	return fmt.Sprintf("invalid protocol range: this side %s, the other side %s", e.Mine, e.Theirs)
}

// Negotiate returns the highest version both ranges hold.
func Negotiate(mine, theirs Range) (int, error) {
	if !mine.Valid() || !theirs.Valid() {
		return 0, &ProtoError{Mine: mine, Theirs: theirs}
	}
	v := min(mine.Max, theirs.Max)
	if v < max(mine.Min, theirs.Min) {
		return 0, &ProtoError{Mine: mine, Theirs: theirs}
	}
	return v, nil
}
