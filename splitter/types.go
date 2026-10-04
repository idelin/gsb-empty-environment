package splitter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Location identifies one of the two routing targets.
type Location string

const (
	Online  Location = "online"
	Archive Location = "archive"
)

// Event is a single incoming business record mutation.
type Event struct {
	Key       string `json:"key"`
	Version   uint64 `json:"version"`
	Content   string `json:"content"`
	IsDeleted bool   `json:"is_deleted"`
}

// Hash is the content fingerprint used for idempotency / conflict checks.
func (e Event) Hash() string { return ContentHash(e.Content) }

// Dest returns the target this event must be routed to.
func (e Event) Dest() Location {
	if e.IsDeleted {
		return Archive
	}
	return Online
}

// Record is a full-content copy of one version of a key, stored in a target.
type Record struct {
	Key       string `json:"key"`
	Version   uint64 `json:"version"`
	Content   string `json:"content"`
	IsDeleted bool   `json:"is_deleted"`
	Hash      string `json:"hash"`
}

// Pointer is the committed read view of a key: exactly one confirmed location.
type Pointer struct {
	Key       string   `json:"key"`
	Version   uint64   `json:"version"`
	Location  Location `json:"location"`
	Hash      string   `json:"hash"`
	IsDeleted bool     `json:"is_deleted"`
}

// Sentinel errors (Go compares these with ==).
var (
	ErrStale    = errors.New("stale version: a higher version is already committed")
	ErrConflict = errors.New("conflict: same version with different content")
	ErrNotFound = errors.New("not found")
)

// ContentHash fingerprints record content.
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
