package splitter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Location identifies one of the two routing targets.
type Location string

const (
	LocationOnline  Location = "online"
	LocationArchive Location = "archive"
)

// Event is a single incoming business record mutation.
type Event struct {
	Key       string
	Version   uint64
	Content   []byte
	IsDeleted bool
}

// Location returns the target a record with this deletion state belongs to.
func (e Event) Location() Location {
	if e.IsDeleted {
		return LocationArchive
	}
	return LocationOnline
}

// Hash returns the content fingerprint used for idempotency/conflict checks.
func (e Event) Hash() string { return HashContent(e.Content) }

// HashContent fingerprints blob content.
func HashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Pointer is the single confirmed location a query is allowed to look at.
// It is committed atomically and is the only thing readers consult.
type Pointer struct {
	Key      string   `json:"key"`
	Version  uint64   `json:"version"`
	Hash     string   `json:"hash"`
	Deleted  bool     `json:"deleted"`
	Location Location `json:"location"`
}

// Phase tracks how far a persisted migration intent has progressed.
type Phase string

const (
	PhaseIntent    Phase = "INTENT"    // intent persisted, blob not yet confirmed
	PhaseCopied    Phase = "COPIED"    // blob durable in the new location
	PhaseCommitted Phase = "COMMITTED" // pointer committed, old copy may remain
)

// Intent is the journaled migration plan. It carries the full content so
// recovery can finish a write whose acknowledgement was lost.
type Intent struct {
	Key          string   `json:"key"`
	Version      uint64   `json:"version"`
	Hash         string   `json:"hash"`
	Deleted      bool     `json:"deleted"`
	Location     Location `json:"location"`
	Content      []byte   `json:"content"`
	Phase        Phase    `json:"phase"`
	HasPrev      bool     `json:"has_prev"`
	PrevLocation Location `json:"prev_location,omitempty"`
	PrevVersion  uint64   `json:"prev_version,omitempty"`
}

// ErrNotFound is returned when a key has no committed pointer.
var ErrNotFound = errors.New("splitter: not found")

// ErrConflict matches same-version/different-content violations.
var ErrConflict = errors.New("splitter: version conflict")

// ConflictError describes a same-version write with diverging content.
type ConflictError struct {
	Key          string
	Version      uint64
	ExistingHash string
	IncomingHash string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("splitter: conflict on key %q version %d: existing hash %.12s, incoming hash %.12s",
		e.Key, e.Version, e.ExistingHash, e.IncomingHash)
}

// Is lets errors.Is(err, ErrConflict) match.
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// BlobName maps (key, version) to a blob-store object name.
func BlobName(key string, version uint64) string {
	return url.PathEscape(key) + "@" + strconv.FormatUint(version, 10)
}

// ParseBlobName reverses BlobName.
func ParseBlobName(name string) (key string, version uint64, err error) {
	idx := strings.LastIndex(name, "@")
	if idx <= 0 {
		return "", 0, fmt.Errorf("splitter: bad blob name %q", name)
	}
	key, err = url.PathUnescape(name[:idx])
	if err != nil {
		return "", 0, fmt.Errorf("splitter: bad blob name %q: %w", name, err)
	}
	version, err = strconv.ParseUint(name[idx+1:], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("splitter: bad blob name %q: %w", name, err)
	}
	return key, version, nil
}
