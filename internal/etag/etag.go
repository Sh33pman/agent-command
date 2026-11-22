// Package etag makes the versions form edits carry (design 8.4): a short
// hash of what a client loaded. A save that sends one back is refused with
// errcode.Conflict when the thing has changed since, by another client or
// by hand.
package etag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Of is v's version: the same for the same JSON, and different, all but
// certainly, for any other.
func Of(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
