//go:build !libghostty

package vt

import "errors"

// Available reports whether this binary has libghostty-vt.
const Available = false

// Terminal is unavailable in a build without the libghostty tag.
type Terminal struct{}

var errUnavailable = errors.New("vt: built without libghostty")

// New always fails in this build.
func New(_, _, _ int, _ func([]byte)) (*Terminal, error) { return nil, errUnavailable }

// Write does nothing in this build.
func (*Terminal) Write([]byte) {}

// Resize fails in this build.
func (*Terminal) Resize(_, _ int) error { return errUnavailable }

// Snapshot fails in this build.
func (*Terminal) Snapshot() ([]byte, error) { return nil, errUnavailable }

// Text fails in this build.
func (*Terminal) Text() (string, error) { return "", errUnavailable }

// Close does nothing in this build.
func (*Terminal) Close() {}
